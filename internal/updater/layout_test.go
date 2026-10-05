package updater

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeLoader(t *testing.T) {
	cases := []struct{ in, want LoaderConfig }{
		{LoaderConfig{Kind: "fabric", Version: "1"}, LoaderConfig{Kind: "fabric", Version: "1", Layout: "installer"}},
		{LoaderConfig{Kind: "fabric", Layout: "launcher"}, LoaderConfig{Kind: "fabric", Layout: "launcher", LauncherFile: "server.jar"}},
		{LoaderConfig{Kind: "fabric", Layout: "launcher", LauncherFile: "fabric.jar"}, LoaderConfig{Kind: "fabric", Layout: "launcher", LauncherFile: "fabric.jar"}},
		{LoaderConfig{Kind: "quilt", Version: "1"}, LoaderConfig{Kind: "quilt", Version: "1"}},
		{LoaderConfig{}, LoaderConfig{}},
	}
	for _, c := range cases {
		if got := normalizeLoader(c.in); got != c.want {
			t.Errorf("%+v: got %+v want %+v", c.in, got, c.want)
		}
	}
}

func TestLayoutValidation(t *testing.T) {
	valid := []LoaderConfig{
		{Kind: "fabric", Version: "latest"},
		{Kind: "fabric", Version: "latest", Layout: "installer"},
		{Kind: "fabric", Version: "latest", Layout: "launcher"},
		{Kind: "fabric", Version: "latest", Layout: "launcher", LauncherFile: "server.jar"},
	}
	invalid := []LoaderConfig{
		{Kind: "quilt", Version: "latest", Layout: "launcher"},
		{Kind: "forge", Version: "latest", LauncherFile: "server.jar"},
		{Kind: "fabric", Version: "latest", Layout: "shim"},
		{Kind: "fabric", Version: "latest", Layout: "installer", LauncherFile: "server.jar"},
		{Kind: "fabric", Version: "latest", Layout: "launcher", LauncherFile: "../server.jar"},
		{Kind: "fabric", Version: "latest", Layout: "launcher", LauncherFile: "server.zip"},
	}
	for _, l := range valid {
		if err := ValidateConfig(Config{Minecraft: "1.21.1", Loader: l}); err != nil {
			t.Errorf("%+v rejected: %v", l, err)
		}
	}
	for _, l := range invalid {
		if err := ValidateConfig(Config{Minecraft: "1.21.1", Loader: l}); err == nil {
			t.Errorf("%+v accepted", l)
		}
	}
}

func TestPrintLayoutConversion(t *testing.T) {
	p := Plan{
		Server:   ServerRelease{Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "0.16.0", Layout: "launcher", LauncherFile: "server.jar"}, InstallerVersion: "1.0.0", JavaMajor: 21},
		Previous: State{Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "0.16.0", Layout: "installer"}},
		Conversion: &LayoutConversion{From: "installer", To: "launcher (server.jar)", Write: []string{"server.jar"},
			Remove: []string{"fabric-server-launch.jar", "libraries/a.jar", "libraries/b/c.jar", "libraries/d.jar"}},
		Changed: true,
	}
	var out strings.Builder
	p.Print(&out)
	for _, want := range []string{
		"Target: Minecraft 1.21.1, fabric 0.16.0, launcher (server.jar, installer 1.0.0) (Java 21+)",
		"Installed: Minecraft 1.21.1, fabric 0.16.0, installer",
		"Convert Fabric layout: installer -> launcher (server.jar)",
		"remove (moved to backup): fabric-server-launch.jar, libraries/ (3 files)",
		"write: server.jar",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

// installerLayoutState fakes a completed installer-layout update, so conversion
// can be tested without running the official Fabric installer (which needs Java).
func installerLayoutState(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{}
	for name, body := range map[string]string{
		"server.jar": "vanilla server", "fabric-server-launch.jar": "shim",
		"libraries/net/fabricmc/fabric-loader/0.16.0/fabric-loader-0.16.0.jar": "loader",
		"libraries/org/ow2/asm/asm/9.8/asm-9.8.jar":                            "asm",
	} {
		putFixture(t, root, name, body)
		digest, err := fileHash(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = digest
	}
	putFixture(t, root, "libraries/unmanaged/keep.txt", "not ours")
	state := State{Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "0.16.0", Layout: "installer"},
		Mods: map[string]InstalledMod{}, Files: files, Start: "java -jar fabric-server-launch.jar nogui"}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	putFixture(t, root, ".mcupdater/state.json", string(data))
}

func TestConvertInstallerToLauncher(t *testing.T) {
	f := newLauncherFixture(t)
	root := t.TempDir()
	installerLayoutState(t, root)
	config := writeLauncherConfig(t, root, nil)
	out, err := runCLI(t, f.Client, "", "check", "-config", config)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Convert Fabric layout: installer -> launcher (server.jar)",
		"remove (moved to backup): fabric-server-launch.jar, libraries/ (2 files)", "write: server.jar"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if _, err = runCLI(t, f.Client, "y\n", "update", "-config", config); err != nil {
		t.Fatal(err)
	}
	assertFile(t, root, "server.jar", string(f.launchers["1.0.0/0.16.0/1.21.1"]))
	for _, gone := range []string{"fabric-server-launch.jar", "libraries/net", "libraries/org"} {
		if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Errorf("%s still present after conversion: %v", gone, err)
		}
	}
	assertFile(t, root, "libraries/unmanaged/keep.txt", "not ours")
	backup := backupDirs(t, root)[0]
	assertFile(t, backup, "old/server.jar", "vanilla server")
	assertFile(t, backup, "old/fabric-server-launch.jar", "shim")
	assertFile(t, backup, "old/libraries/org/ow2/asm/asm/9.8/asm-9.8.jar", "asm")
	if out, err = runCLI(t, f.Client, "", "check", "-config", config); err != nil || !strings.Contains(out, "Already up to date") || strings.Contains(out, "Convert") {
		t.Fatalf("conversion not idempotent: %v\n%s", err, out)
	}
}

func TestRenameLauncherFile(t *testing.T) {
	f := newLauncherFixture(t)
	root := t.TempDir()
	config := writeLauncherConfig(t, root, nil)
	if _, err := runCLI(t, f.Client, "y\n", "update", "-config", config); err != nil {
		t.Fatal(err)
	}
	editConfig(t, config, func(c *Config) { c.Loader.LauncherFile = "fabric.jar" })
	out, err := runCLI(t, f.Client, "y\n", "update", "-config", config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Convert Fabric layout: launcher (server.jar) -> launcher (fabric.jar)") || !strings.Contains(out, "remove (moved to backup): server.jar") {
		t.Fatalf("rename not shown:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "server.jar")); !os.IsNotExist(err) {
		t.Fatalf("old launcher left behind: %v", err)
	}
	assertFile(t, root, "fabric.jar", string(f.launchers["1.0.0/0.16.0/1.21.1"]))
}
