//go:build functional

// These tests download official Minecraft/loader artifacts and require Java 21.
// Run: go test -tags functional -count=1 -timeout=30m -v ./tests/functional
// Set MCUPDATER_TEST_LOADER to run one server type. No Minecraft server is started.
package functional_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/quanchobi/mcupdater/internal/updater"
)

func TestCLI(t *testing.T) {
	loaders := []updater.LoaderConfig{
		{Kind: "vanilla"},
		{Kind: "fabric", Version: "0.16.14"},
		{Kind: "quilt", Version: "0.27.1"},
		{Kind: "forge", Version: "52.0.47"},
		{Kind: "neoforge", Version: "21.1.172"},
	}
	if selected := os.Getenv("MCUPDATER_TEST_LOADER"); selected != "" {
		found := false
		for _, loader := range loaders {
			if loader.Kind == selected {
				loaders, found = []updater.LoaderConfig{loader}, true
				break
			}
		}
		if !found {
			t.Fatalf("unknown MCUPDATER_TEST_LOADER %q", selected)
		}
	}
	java, err := exec.LookPath("java")
	if err != nil {
		t.Fatal("functional tests require Java 21 on PATH: ", err)
	}
	java, err = filepath.Abs(java)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CURSEFORGE_API_KEY", "")
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "mcupdater")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	run(t, repo, "", true, "go", "build", "-o", binary, ".")
	for _, loader := range loaders {
		t.Run(loader.Kind, func(t *testing.T) {
			testLifecycle(t, binary, java, loader)
		})
	}
}

func testLifecycle(t *testing.T, binary, java string, loader updater.LoaderConfig) {
	t.Helper()
	// Spaces exercise installer argument handling. Config and invocation directory
	// are separate so relative server paths cannot accidentally resolve via cwd.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := filepath.Join(root, "server with spaces")
	cwd := filepath.Join(root, "unrelated working directory")
	config := filepath.Join(root, "config", "mcupdater.json")
	for _, dir := range []string{server, cwd, filepath.Dir(config)} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	preserved := map[string]string{
		"world/level.dat":         "world must never be touched",
		"config/admin.properties": "administrator settings",
		"server.properties":       "motd=functional test\n",
		"eula.txt":                "eula=false\n",
		"user_jvm_args.txt":       "-Xmx1G\n",
	}
	for name, data := range preserved {
		put(t, filepath.Join(server, filepath.FromSlash(name)), data)
	}
	replaced := "server.jar"
	if loader.Kind == "forge" || loader.Kind == "neoforge" {
		replaced = "run.sh"
		if runtime.GOOS == "windows" {
			replaced = "run.bat"
		}
	}
	put(t, filepath.Join(server, replaced), "previous installation")

	t.Log("initializing and checking without changing server files")
	run(t, cwd, "", true, binary, "init", "-config", config, "-server-dir", server,
		"-minecraft", "1.21.1", "-loader", loader.Kind, "-loader-version", loader.Version, "-java", java)
	t.Log("refusing to overwrite an unacknowledged pre-existing server file")
	pristine := snapshot(t, server)
	// Forge/NeoForge outputs are only known after their installer runs, so check
	// cannot predict run.sh; update must still refuse before touching live files.
	predictable := loader.Kind != "forge" && loader.Kind != "neoforge"
	run(t, cwd, "", !predictable, binary, "check", "-config", config)
	run(t, cwd, "y\n", false, binary, "update", "-config", config)
	unchanged(t, server, pristine)

	var cfg updater.Config
	readJSON(t, config, &cfg)
	cfg.ServerDir = "../server with spaces"
	// The suite seeds a pre-existing file at the installer's output path.
	cfg.ReplaceUnmanaged = []string{replaced}
	if loader.Kind == "fabric" {
		cfg.Mods = []updater.Mod{{Platform: "modrinth", ProjectID: "P7dR8mSH"}} // Fabric API, no API key needed.
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, server)
	run(t, cwd, "", true, binary, "check", "-config", config)
	unchanged(t, server, before)

	t.Log("declining and withholding update confirmation")
	run(t, cwd, "n\n", true, binary, "update", "-config", config)
	unchanged(t, server, before)
	run(t, cwd, "", true, binary, "update", "-config", config)
	unchanged(t, server, before)

	t.Log("installing official server artifacts")
	run(t, cwd, "y\n", true, binary, "update", "-config", config)
	var state updater.State
	readJSON(t, filepath.Join(server, ".mcupdater", "state.json"), &state)
	if state.Minecraft != "1.21.1" || state.Loader != loader {
		t.Fatalf("wrong release installed: Minecraft %s, loader %+v", state.Minecraft, state.Loader)
	}
	for name, want := range preserved {
		expectFile(t, filepath.Join(server, filepath.FromSlash(name)), want)
	}
	backups, err := os.ReadDir(filepath.Join(server, ".mcupdater", "backups"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 || !backups[0].IsDir() {
		t.Fatalf("expected one recovery backup, got %v", backups)
	}
	expectFile(t, filepath.Join(server, ".mcupdater", "backups", backups[0].Name(), "old", replaced), "previous installation")

	// The JVM checks the installed class/module path without invoking main().
	// This catches broken staging-relative launchers without accepting the EULA.
	t.Log("validating the installed launch path with Java --dry-run")
	args := []string{"--dry-run"}
	if loader.Kind == "vanilla" {
		args = append(args, "-jar", "server.jar", "nogui")
	} else if loader.Kind == "fabric" || loader.Kind == "quilt" {
		args = append(args, "-jar", loader.Kind+"-server-launch.jar", "nogui")
	} else {
		argsFile := "/unix_args.txt"
		if runtime.GOOS == "windows" {
			argsFile = "/win_args.txt"
		}
		var matches []string
		for name := range state.Files {
			if strings.HasPrefix(name, "libraries/") && strings.HasSuffix(name, argsFile) {
				matches = append(matches, name)
			}
		}
		if len(matches) != 1 {
			t.Fatalf("expected one installed Java argument file, got %v", matches)
		}
		args = append(args, "@user_jvm_args.txt", "@"+matches[0], "nogui")
	}
	run(t, server, "", true, java, args...)
	expectFile(t, filepath.Join(server, "eula.txt"), preserved["eula.txt"])
	expectFile(t, filepath.Join(server, "world", "level.dat"), preserved["world/level.dat"])

	if loader.Kind == "fabric" {
		t.Log("discovering the installed mod by its downloaded content")
		discovered := filepath.Join(root, "discovered.json")
		run(t, cwd, "", true, binary, "init", "-config", discovered, "-server-dir", server, "-loader", loader.Kind)
		var inventory updater.Config
		readJSON(t, discovered, &inventory)
		if len(inventory.Mods) != 1 || inventory.Mods[0].Platform != "modrinth" || inventory.Mods[0].ProjectID != "P7dR8mSH" {
			t.Fatalf("installed Fabric API was not discovered: %+v", inventory.Mods)
		}
		installed := state.Mods["modrinth:P7dR8mSH"]
		if inventory.Mods[0].File != installed.File {
			t.Fatalf("discovery did not identify the installed mod JAR: %+v", inventory.Mods[0])
		}
	}

	t.Log("checking repeat updates leave installed files and backups unchanged")
	installed := snapshot(t, server)
	run(t, cwd, "", true, binary, "check", "-config", config)
	unchanged(t, server, installed)
	// Confirm deliberately: a spurious Changed flag would reinstall and create
	// another backup, rather than merely cancel and conceal a no-op regression.
	run(t, cwd, "y\n", true, binary, "update", "-config", config)
	unchanged(t, server, installed)

	t.Log("refusing an untracked mod without changing the installation")
	put(t, filepath.Join(server, "mods", "untracked.jar"), "untracked user file")
	withUntracked := snapshot(t, server)
	run(t, cwd, "", false, binary, "check", "-config", config)
	unchanged(t, server, withUntracked)
	run(t, cwd, "y\n", false, binary, "update", "-config", config)
	unchanged(t, server, withUntracked)
}

func run(t *testing.T, cwd, stdin string, success bool, program string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir, cmd.Stdin, cmd.WaitDelay = cwd, strings.NewReader(stdin), 5*time.Second
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s %v timed out: %v\n%s", program, args, ctx.Err(), output)
	}
	if success {
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", program, args, err, output)
		}
		return
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("expected CLI rejection from %s %v; got %v\n%s", program, args, err, output)
	}
}

func put(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, name string, value any) {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
}

func expectFile(t *testing.T, name, want string) {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil || string(data) != want {
		t.Fatalf("%s: got %q, %v; want %q", name, data, err, want)
	}
}

func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		files[rel] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func unchanged(t *testing.T, root string, before map[string]string) {
	t.Helper()
	after := snapshot(t, root)
	for name, digest := range before {
		if after[name] != digest {
			t.Errorf("file modified or removed: %s", name)
		}
	}
	for name := range after {
		if _, exists := before[name]; !exists {
			t.Errorf("unexpected file created: %s", name)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}
