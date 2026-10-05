package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func zipRaw(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		f, err := w.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testInstallerEntries(version string) []entry {
	manifest := strings.Replace(testManifest, "Implementation-Version: 1.1.2", "Implementation-Version: "+version, 1)
	return []entry{{"META-INF/MANIFEST.MF", manifest}, {"net/", ""}, {"net/fabricmc/A.class", "class bytes " + version}}
}

func testLauncherBytes(t *testing.T, installer, loader, game string, extra ...entry) []byte {
	entries := append(testInstallerEntries(installer), entry{"install.properties", "fabric-loader-version=" + loader + "\ngame-version=" + game})
	return zipRaw(t, append(entries, extra...)...)
}

// launcherFixture extends plannerFixture (Minecraft 1.21.1, Fabric loader
// 0.16.0) with Fabric installer metadata, checksummed Maven installer
// -server.jars, and meta-generated launchers. Launchers maps
// "installer/loader/game" to served bytes; tests may replace entries.
type launcherFixture struct {
	*Client
	mu        sync.Mutex
	launchers map[string][]byte
	requests  []string
}

func newLauncherFixture(t *testing.T) *launcherFixture {
	t.Helper()
	f := &launcherFixture{Client: plannerFixture(t, nil, 0), launchers: map[string][]byte{}}
	installers := map[string][]byte{}
	for _, v := range []string{"0.9.0", "1.0.0"} {
		installers[v] = zipRaw(t, testInstallerEntries(v)...)
	}
	f.launchers["1.0.0/0.16.0/1.21.1"] = testLauncherBytes(t, "1.0.0", "0.16.0", "1.21.1")
	base := f.HTTP.Transport
	f.HTTP.Transport = planTransport(func(r *http.Request) (*http.Response, error) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.String())
		f.mu.Unlock()
		respond := func(status int, raw []byte) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(raw)), ContentLength: int64(len(raw)), Request: r}, nil
		}
		path := r.URL.Path
		switch {
		case r.URL.Host == "meta.fabricmc.net" && path == "/v2/versions/installer":
			data, _ := json.Marshal([]any{map[string]any{"version": "1.0.0", "stable": true, "url": "https://maven.fabricmc.net/net/fabricmc/fabric-installer/1.0.0/fabric-installer-1.0.0.jar"}})
			return respond(200, data)
		case r.URL.Host == "meta.fabricmc.net" && strings.HasSuffix(path, "/server/jar"):
			parts := strings.Split(strings.TrimPrefix(path, "/v2/versions/loader/"), "/")
			f.mu.Lock()
			raw, ok := f.launchers[parts[2]+"/"+parts[1]+"/"+parts[0]]
			f.mu.Unlock()
			if !ok {
				return respond(400, []byte("bad request"))
			}
			return respond(200, raw)
		case r.URL.Host == "maven.fabricmc.net":
			name := filepath.Base(path)
			for v, raw := range installers {
				jar := "fabric-installer-" + v + "-server.jar"
				switch name {
				case jar:
					return respond(200, raw)
				case jar + ".sha1":
					sum := sha1.Sum(raw)
					return respond(200, []byte(hex.EncodeToString(sum[:])))
				case jar + ".sha256", jar + ".sha512":
					return respond(404, nil)
				}
			}
			return respond(404, nil)
		}
		return base.RoundTrip(r)
	})
	return f
}

func (f *launcherFixture) count(substr string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.Contains(r, substr) {
			n++
		}
	}
	return n
}

var launcherConfig = Config{Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "latest", Layout: "launcher", LauncherFile: "server.jar"}}

func TestFabricLauncherResolveAndInstall(t *testing.T) {
	f := newLauncherFixture(t)
	ctx := context.Background()
	release, err := f.ResolveServer(ctx, launcherConfig)
	if err != nil {
		t.Fatal(err)
	}
	if release.InstallerVersion != "1.0.0" || release.Loader.Version != "0.16.0" || release.Launcher.Filename != "server.jar" {
		t.Fatalf("unexpected release %+v", release)
	}
	if release.LauncherInstaller.HashAlgorithm != "sha1" || release.LauncherInstaller.Hash == "" {
		t.Fatalf("installer -server.jar is not checksummed: %+v", release.LauncherInstaller)
	}
	stage := filepath.Join(t.TempDir(), "stage")
	// A Java path that does not exist proves installation runs no Java.
	java := filepath.Join(t.TempDir(), "no-java-here")
	command, err := f.InstallServer(ctx, release, stage, java, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if want := loaderShellQuote(java) + " -jar server.jar nogui"; command != want {
		t.Fatalf("command %q, want %q", command, want)
	}
	entries, err := os.ReadDir(stage)
	if err != nil || len(entries) != 1 || entries[0].Name() != "server.jar" {
		t.Fatalf("stage must contain only server.jar: %v %v", entries, err)
	}
	assertFile(t, stage, "server.jar", string(f.launchers["1.0.0/0.16.0/1.21.1"]))

	f.launchers["1.0.0/0.16.0/1.21.1"] = testLauncherBytes(t, "1.0.0", "0.16.0", "1.21.1", entry{"evil/Payload.class", "x"})
	stage = filepath.Join(t.TempDir(), "stage")
	if _, err = f.InstallServer(ctx, release, stage, "java", io.Discard); err == nil || !strings.Contains(err.Error(), "verify Fabric launcher") {
		t.Fatalf("tampered launcher accepted: %v", err)
	}
	if _, err = os.Stat(filepath.Join(stage, "server.jar")); !os.IsNotExist(err) {
		t.Fatalf("tampered launcher reached the stage: %v", err)
	}
}

func TestFabricLauncherInstallerVersionChangesPlan(t *testing.T) {
	f := newLauncherFixture(t)
	root := t.TempDir()
	cfg := launcherConfig
	cfg.ServerDir = root
	ctx := context.Background()
	release, err := Lock(root) // as Run does; creates .mcupdater/
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	p, err := f.BuildPlan(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Apply(ctx, cfg, p, io.Discard); err != nil {
		t.Fatal(err)
	}
	state, err := readState(root)
	if err != nil || state.Installer != "1.0.0" || state.Loader.Layout != "launcher" {
		t.Fatalf("state %+v, %v", state, err)
	}
	if p, err = f.BuildPlan(ctx, cfg); err != nil || p.Changed {
		t.Fatalf("not idempotent: changed=%v %v", p.Changed, err)
	}
	state.Installer = "0.9.0"
	data, _ := json.Marshal(state)
	putFixture(t, root, ".mcupdater/state.json", string(data))
	if p, err = f.BuildPlan(ctx, cfg); err != nil || !p.Changed {
		t.Fatalf("installer upgrade not planned: changed=%v %v", p.Changed, err)
	}
}

// writeLauncherConfig writes a launcher-layout config for root and returns its path.
func writeLauncherConfig(t *testing.T, root string, edit func(*Config)) string {
	t.Helper()
	cfg := launcherConfig
	cfg.ServerDir = root
	if edit != nil {
		edit(&cfg)
	}
	config := filepath.Join(t.TempDir(), "mcupdater.json")
	if err := WriteConfig(config, cfg); err != nil {
		t.Fatal(err)
	}
	return config
}

func runCLI(t *testing.T, c *Client, stdin string, args ...string) (string, error) {
	t.Helper()
	var out strings.Builder
	err := Run(context.Background(), args, strings.NewReader(stdin), &out, io.Discard, c)
	return out.String(), err
}

func backupDirs(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".mcupdater", "backups"))
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		dirs = append(dirs, filepath.Join(root, ".mcupdater", "backups", e.Name()))
	}
	return dirs
}

func TestAdoptVerifiedExistingLauncher(t *testing.T) {
	f := newLauncherFixture(t)
	root := t.TempDir()
	old := testLauncherBytes(t, "0.9.0", "0.15.0", "1.21.1")
	putFixture(t, root, "server.jar", string(old))
	config := writeLauncherConfig(t, root, nil)

	out, err := runCLI(t, f.Client, "", "check", "-config", config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Adopt Fabric launcher server.jar: verified as fabric-installer 0.9.0 (loader 0.15.0, Minecraft 1.21.1)") {
		t.Fatalf("adoption not shown:\n%s", out)
	}
	assertFile(t, root, "server.jar", string(old))
	if f.count("fabric-installer-0.9.0-server.jar") == 0 {
		t.Fatal("existing launcher was not verified against its own Maven installer")
	}
	if _, err = runCLI(t, f.Client, "y\n", "update", "-config", config); err != nil {
		t.Fatal(err)
	}
	assertFile(t, root, "server.jar", string(f.launchers["1.0.0/0.16.0/1.21.1"]))
	backups := backupDirs(t, root)
	if len(backups) != 1 {
		t.Fatalf("backups %v", backups)
	}
	assertFile(t, backups[0], "old/server.jar", string(old))
	if out, err = runCLI(t, f.Client, "", "check", "-config", config); err != nil || !strings.Contains(out, "Already up to date") {
		t.Fatalf("not idempotent after adoption: %v\n%s", err, out)
	}
}

func TestAdoptionRejectsTamperedLauncher(t *testing.T) {
	f := newLauncherFixture(t)
	root := t.TempDir()
	tampered := zipRaw(t, entry{"META-INF/MANIFEST.MF", strings.Replace(testManifest, "1.1.2", "0.9.0", 1)}, entry{"net/", ""},
		entry{"net/fabricmc/A.class", "tampered"}, entry{"install.properties", "fabric-loader-version=0.15.0\ngame-version=1.21.1"})
	putFixture(t, root, "server.jar", string(tampered))
	config := writeLauncherConfig(t, root, nil)
	_, err := runCLI(t, f.Client, "", "check", "-config", config)
	if err == nil || !strings.Contains(err.Error(), "could not be verified") || !strings.Contains(err.Error(), "replace_unmanaged") {
		t.Fatalf("tampered launcher not refused: %v", err)
	}
	if _, err = runCLI(t, f.Client, "y\n", "update", "-config", config); err == nil {
		t.Fatal("update accepted tampered launcher")
	}
	assertFile(t, root, "server.jar", string(tampered))
	assertNoBackups(t, root)
	// Acknowledging the file skips adoption and replaces it.
	setReplaceUnmanaged(t, config, "server.jar")
	if _, err = runCLI(t, f.Client, "y\n", "update", "-config", config); err != nil {
		t.Fatal(err)
	}
	assertFile(t, root, "server.jar", string(f.launchers["1.0.0/0.16.0/1.21.1"]))
}

func TestNonLauncherAtLauncherPathIsACollision(t *testing.T) {
	f := newLauncherFixture(t)
	root := t.TempDir()
	putFixture(t, root, "server.jar", "vanilla or something else")
	config := writeLauncherConfig(t, root, nil)
	_, err := runCLI(t, f.Client, "", "check", "-config", config)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("non-launcher not treated as collision: %v", err)
	}
	if f.count("fabric-installer-0.9.0") != 0 {
		t.Fatalf("non-launcher triggered adoption lookups: %v", f.requests)
	}
	setReplaceUnmanaged(t, config, "server.jar")
	if _, err = runCLI(t, f.Client, "y\n", "update", "-config", config); err != nil {
		t.Fatal(err)
	}
}

// withMods serves Modrinth versions and artifact bytes for the given projects
// on top of a launcher fixture. bodies maps project ID to served JAR bytes.
func (f *launcherFixture) withMods(t *testing.T, bodies map[string]string) {
	t.Helper()
	versions := map[string][]modrinthVersion{}
	for id, body := range bodies {
		v := fixtureVersion(id)
		v.Files[0].Hashes = map[string]string{"sha1": sha1Hex(body)}
		v.Files[0].Size = int64(len(body))
		versions[id] = []modrinthVersion{v}
	}
	mods := plannerFixture(t, versions, 0).HTTP.Transport
	launcher := f.HTTP.Transport
	f.HTTP.Transport = planTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "fixture.invalid" {
			if body, ok := bodies[strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".jar")]; ok {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
			}
		}
		if r.URL.Host == "api.modrinth.com" {
			return mods.RoundTrip(r)
		}
		return launcher.RoundTrip(r)
	})
}

// A first update on an existing server re-stages unchanged mods under their
// current filenames. Those JARs are tracked by identity (config file), so the
// file-ownership rule must not treat them as foreign.
func TestFirstUpdateKeepsIdentityTrackedMods(t *testing.T) {
	f := newLauncherFixture(t)
	f.withMods(t, map[string]string{"a": "jar a", "b": "jar b"})
	root := t.TempDir()
	putFixture(t, root, "mods/a.jar", "jar a")     // same filename as the selected release
	putFixture(t, root, "mods/b-old.jar", "old b") // different filename: replaced
	config := writeLauncherConfig(t, root, func(c *Config) {
		c.Mods = []Mod{{Platform: "modrinth", ProjectID: "a", File: "a.jar"}, {Platform: "modrinth", ProjectID: "b", File: "b-old.jar"}}
	})
	if _, err := runCLI(t, f.Client, "", "check", "-config", config); err != nil {
		t.Fatalf("check refused identity-tracked mods: %v", err)
	}
	if _, err := runCLI(t, f.Client, "y\n", "update", "-config", config); err != nil {
		t.Fatalf("update refused identity-tracked mods: %v", err)
	}
	assertFile(t, root, "mods/a.jar", "jar a")
	assertFile(t, root, "mods/b.jar", "jar b")
	if _, err := os.Stat(filepath.Join(root, "mods", "b-old.jar")); !os.IsNotExist(err) {
		t.Fatalf("old b left behind: %v", err)
	}
	assertFile(t, backupDirs(t, root)[0], "old/mods/b-old.jar", "old b")
}
