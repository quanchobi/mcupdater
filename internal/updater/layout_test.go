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

func TestDetectFabricLayout(t *testing.T) {
	launcher := func(loader, game string) string {
		return string(zipRaw(t, append(testInstallerEntries("1.1.0"), entry{"install.properties", "fabric-loader-version=" + loader + "\ngame-version=" + game})...))
	}
	cases := []struct {
		name, layout, file string
		files              map[string]string
		want               LoaderConfig
		errHas             []string
	}{
		{name: "empty auto", layout: "auto", want: LoaderConfig{Layout: "installer"}},
		{name: "empty launcher", layout: "launcher", want: LoaderConfig{Layout: "launcher", LauncherFile: "server.jar"}},
		{name: "explicit installer ignores launcher", layout: "installer", files: map[string]string{"server.jar": launcher("0.18.1", "1.21.10")}, want: LoaderConfig{Layout: "installer"}},
		{name: "one launcher", layout: "auto", files: map[string]string{"server.jar": launcher("0.18.1", "1.21.10"), "notes.jar": "not a zip"},
			want: LoaderConfig{Layout: "launcher", LauncherFile: "server.jar"}},
		{name: "vanilla and shim are not launchers", layout: "auto", files: map[string]string{"server.jar": string(zipRaw(t, entry{"META-INF/MANIFEST.MF", "Main-Class: net.minecraft.bundler.Main\n"}))},
			want: LoaderConfig{Layout: "installer"}},
		{name: "two launchers (production)", layout: "auto",
			files:  map[string]string{"server.jar": launcher("0.18.1", "1.21.10"), "fabric-server-launch.jar": launcher("0.17.2", "1.21.6")},
			errHas: []string{"server.jar (loader 0.18.1, Minecraft 1.21.10)", "fabric-server-launch.jar (loader 0.17.2, Minecraft 1.21.6)", "-launcher-file"}},
		{name: "two launchers, chosen", layout: "auto", file: "server.jar",
			files: map[string]string{"server.jar": launcher("0.18.1", "1.21.10"), "fabric-server-launch.jar": launcher("0.17.2", "1.21.6")},
			want:  LoaderConfig{Layout: "launcher", LauncherFile: "server.jar"}},
		{name: "chosen file is not a launcher", layout: "auto", file: "server.jar", files: map[string]string{"server.jar": "vanilla"}, errHas: []string{"not a Fabric server launcher"}},
		{name: "chosen file missing", layout: "auto", file: "server.jar", errHas: []string{"server.jar"}},
		{name: "launcher-file with installer layout", layout: "installer", file: "server.jar", errHas: []string{"-launcher-file"}},
		{name: "bad layout", layout: "shim", errHas: []string{"-layout"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for name, body := range c.files {
				putFixture(t, root, name, body)
			}
			got, _, err := detectFabricLayout(root, c.layout, c.file)
			if len(c.errHas) > 0 {
				if err == nil {
					t.Fatalf("accepted: %+v", got)
				}
				for _, want := range c.errHas {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q lacks %q", err, want)
					}
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, c.want)
			}
		})
	}
}

func TestInitRecordsLayout(t *testing.T) {
	root := t.TempDir()
	putFixture(t, root, "server.jar", string(testLauncherBytes(t, "1.1.0", "0.18.1", "1.21.10")))
	config := filepath.Join(t.TempDir(), "mcupdater.json")
	out, err := runCLI(t, &Client{}, "", "init", "-config", config, "-server-dir", root, "-loader", "fabric", "-minecraft", "1.21.10")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Detected Fabric launcher layout: server.jar (installer 1.1.0, loader 0.18.1, Minecraft 1.21.10)") {
		t.Fatalf("detection not reported:\n%s", out)
	}
	cfg, err := LoadConfig(config)
	if err != nil || cfg.Loader.Layout != "launcher" || cfg.Loader.LauncherFile != "server.jar" {
		t.Fatalf("config %+v, %v", cfg.Loader, err)
	}
	raw, _ := os.ReadFile(config)
	if !strings.Contains(string(raw), `"layout": "launcher"`) {
		t.Fatalf("layout not written explicitly:\n%s", raw)
	}
	if _, err = runCLI(t, &Client{}, "", "init", "-config", filepath.Join(t.TempDir(), "q.json"), "-server-dir", root, "-loader", "quilt", "-layout", "launcher"); err == nil {
		t.Fatal("quilt accepted -layout launcher")
	}
}

func TestInitWarnsAboutInstallerCollision(t *testing.T) {
	root := t.TempDir()
	putFixture(t, root, "server.jar", "vanilla")
	out, err := runCLI(t, &Client{}, "", "init", "-config", filepath.Join(t.TempDir(), "c.json"), "-server-dir", root, "-loader", "fabric", "-minecraft", "1.21.1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "server.jar") || !strings.Contains(out, "replace_unmanaged") {
		t.Fatalf("no collision warning:\n%s", out)
	}
}
