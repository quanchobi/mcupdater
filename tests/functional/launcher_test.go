//go:build functional

package functional_test

import (
	"archive/zip"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/quanchobi/mcupdater/internal/updater"
)

// These versions are pinned so the test is reproducible; installer 1.1.0 is
// deliberately older than the latest stable so adoption sees a real upgrade.
const (
	launcherGame   = "1.21.1"
	launcherLoader = "0.16.14"
)

func fetchLauncher(t *testing.T, game, loader, installer string) []byte {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get("https://meta.fabricmc.net/v2/versions/loader/" + game + "/" + loader + "/" + installer + "/server/jar")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fetch launcher: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func jarEntry(t *testing.T, path, entry string) string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	f, err := r.Open(entry)
	if err != nil {
		t.Fatalf("%s: %v", entry, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mustContain(t *testing.T, output string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(output, want) {
			t.Fatalf("output lacks %q:\n%s", want, output)
		}
	}
}

func absent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s should not exist: %v", path, err)
	}
}

func editConfig(t *testing.T, config string, edit func(*updater.Config)) {
	t.Helper()
	var cfg updater.Config
	readJSON(t, config, &cfg)
	edit(&cfg)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestFabricLauncherLayout(t *testing.T) {
	java, binary := javaAndBinary(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := filepath.Join(root, "server with spaces")
	config := filepath.Join(root, "mcupdater.json")
	preserved := map[string]string{"world/level.dat": "world must never be touched", "eula.txt": "eula=false\n", "server.properties": "motd=launcher\n"}
	for name, data := range preserved {
		put(t, filepath.Join(server, filepath.FromSlash(name)), data)
	}
	seeded := fetchLauncher(t, launcherGame, launcherLoader, "1.1.0")
	put(t, filepath.Join(server, "server.jar"), string(seeded))

	t.Log("init detects the existing launcher")
	out := runOutput(t, root, "", true, binary, "init", "-config", config, "-server-dir", server,
		"-minecraft", launcherGame, "-loader", "fabric", "-loader-version", launcherLoader, "-java", java)
	mustContain(t, out, "Detected Fabric launcher layout: server.jar (installer 1.1.0, loader "+launcherLoader+", Minecraft "+launcherGame+")")
	var cfg updater.Config
	readJSON(t, config, &cfg)
	if cfg.Loader.Layout != "launcher" || cfg.Loader.LauncherFile != "server.jar" {
		t.Fatalf("layout not recorded: %+v", cfg.Loader)
	}

	t.Log("check verifies and adopts it without writing")
	before := snapshot(t, server)
	out = runOutput(t, root, "", true, binary, "check", "-config", config)
	mustContain(t, out, "Adopt Fabric launcher server.jar: verified as fabric-installer 1.1.0")
	unchanged(t, server, before)

	t.Log("update replaces it with a verified launcher and backs up the old one")
	run(t, root, "y\n", true, binary, "update", "-config", config)
	var state updater.State
	readJSON(t, filepath.Join(server, ".mcupdater", "state.json"), &state)
	if state.Installer == "" || state.Loader.Layout != "launcher" {
		t.Fatalf("state %+v", state)
	}
	launcher := filepath.Join(server, "server.jar")
	mustContain(t, jarEntry(t, launcher, "install.properties"), "fabric-loader-version="+launcherLoader, "game-version="+launcherGame)
	mustContain(t, jarEntry(t, launcher, "META-INF/MANIFEST.MF"), "Implementation-Version: "+state.Installer)
	backups, err := os.ReadDir(filepath.Join(server, ".mcupdater", "backups"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups %v %v", backups, err)
	}
	expectFile(t, filepath.Join(server, ".mcupdater", "backups", backups[0].Name(), "old", "server.jar"), string(seeded))
	absent(t, filepath.Join(server, "fabric-server-launch.jar"))
	absent(t, filepath.Join(server, "libraries"))
	for name, want := range preserved {
		expectFile(t, filepath.Join(server, filepath.FromSlash(name)), want)
	}

	t.Log("the launcher's main class loads (JVM --dry-run, no server start)")
	run(t, server, "", true, java, "--dry-run", "-jar", "server.jar", "nogui")

	t.Log("repeat check and update are no-ops")
	installed := snapshot(t, server)
	mustContain(t, runOutput(t, root, "", true, binary, "check", "-config", config), "Already up to date.")
	run(t, root, "y\n", true, binary, "update", "-config", config)
	unchanged(t, server, installed)

	t.Log("convert launcher -> installer")
	editConfig(t, config, func(c *updater.Config) { c.Loader.Layout, c.Loader.LauncherFile = "installer", "" })
	out = runOutput(t, root, "", true, binary, "check", "-config", config)
	mustContain(t, out, "Convert Fabric layout: launcher (server.jar) -> installer", "write: server.jar, fabric-server-launch.jar")
	run(t, root, "y\n", true, binary, "update", "-config", config)
	mustContain(t, jarEntry(t, launcher, "META-INF/MANIFEST.MF"), "Main-Class: net.minecraft.bundler.Main")
	run(t, server, "", true, java, "--dry-run", "-jar", "fabric-server-launch.jar", "nogui")

	t.Log("convert installer -> launcher")
	editConfig(t, config, func(c *updater.Config) { c.Loader.Layout, c.Loader.LauncherFile = "launcher", "server.jar" })
	out = runOutput(t, root, "", true, binary, "check", "-config", config)
	mustContain(t, out, "Convert Fabric layout: installer -> launcher (server.jar)", "remove (moved to backup): fabric-server-launch.jar, libraries/ (")
	run(t, root, "y\n", true, binary, "update", "-config", config)
	absent(t, filepath.Join(server, "fabric-server-launch.jar"))
	absent(t, filepath.Join(server, "libraries"))
	mustContain(t, jarEntry(t, launcher, "install.properties"), "fabric-loader-version="+launcherLoader)
	if backups, _ = os.ReadDir(filepath.Join(server, ".mcupdater", "backups")); len(backups) != 3 {
		t.Fatalf("expected 3 backups, got %d", len(backups))
	}
	for name, want := range preserved {
		expectFile(t, filepath.Join(server, filepath.FromSlash(name)), want)
	}
}

// TestFabricLauncherAmbiguity mirrors a production server with a stale second
// launcher: init must refuse to guess, and the unchosen launcher is never touched.
func TestFabricLauncherAmbiguity(t *testing.T) {
	java, binary := javaAndBinary(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := filepath.Join(root, "server")
	put(t, filepath.Join(server, "server.jar"), string(fetchLauncher(t, launcherGame, launcherLoader, "1.1.0")))
	stale := string(fetchLauncher(t, launcherGame, "0.16.10", "1.1.0"))
	put(t, filepath.Join(server, "fabric-server-launch.jar"), stale)
	config := filepath.Join(root, "mcupdater.json")
	out := runOutput(t, root, "", false, binary, "init", "-config", config, "-server-dir", server,
		"-minecraft", launcherGame, "-loader", "fabric", "-loader-version", launcherLoader, "-java", java)
	mustContain(t, out, "server.jar (loader "+launcherLoader+", Minecraft "+launcherGame+")", "fabric-server-launch.jar (loader 0.16.10, Minecraft "+launcherGame+")", "-launcher-file")
	absent(t, config)
	run(t, root, "", true, binary, "init", "-config", config, "-server-dir", server, "-launcher-file", "server.jar",
		"-minecraft", launcherGame, "-loader", "fabric", "-loader-version", launcherLoader, "-java", java)
	run(t, root, "y\n", true, binary, "update", "-config", config)
	expectFile(t, filepath.Join(server, "fabric-server-launch.jar"), stale)
}
