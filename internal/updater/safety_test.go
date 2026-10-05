package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadRejectsCorruptionWithoutLeavingArtifact(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("corrupted artifact")) }))
	defer server.Close()
	expected := sha256.Sum256([]byte("expected artifact!"))
	target := filepath.Join(t.TempDir(), "mod.jar")
	err := NewClient("").Download(context.Background(), Artifact{URL: server.URL, Filename: "mod.jar", HashAlgorithm: "sha256", Hash: hex.EncodeToString(expected[:])}, target)
	if err == nil {
		t.Fatal("corrupt download was accepted")
	}
	if _, err = os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed download left artifact: %v", err)
	}
}

func TestSymlinkedModsCannotEscapeServer(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "important.jar")
	if err := os.WriteFile(target, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "mods")); err != nil {
		t.Skip(err)
	}
	if _, err := inventory(root, Config{}, State{}); err == nil {
		t.Fatal("accepted symlinked mods directory")
	}
	if _, err := safePath(root, "mods/important.jar"); err == nil {
		t.Fatal("accepted path through symlink")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "outside" {
		t.Fatalf("outside file changed: %q %v", data, err)
	}
}

func TestVanillaRejectsUnsupportedConfiguration(t *testing.T) {
	optional := false
	cfg := Config{Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "vanilla"}, Mods: []Mod{{Platform: "modrinth", ProjectID: "example", Mandatory: &optional}}}
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("vanilla accepted a configured mod, even though it cannot load mods")
	}
	cfg.Mods = nil
	cfg.Loader.Version = "0.16.14"
	if err := ValidateConfig(cfg); err == nil {
		t.Fatal("vanilla accepted a loader version pin instead of a Minecraft version")
	}
}

func TestVanillaInitRejectsDiscoveredMods(t *testing.T) {
	root := t.TempDir()
	putFixture(t, root, "mods/installed.jar", "x")
	client := plannerFixture(t, nil, 0)
	base := client.HTTP.Transport
	client.HTTP.Transport = planTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.modrinth.com" && strings.HasPrefix(r.URL.Path, "/v2/version_file/") {
			raw := `{"id":"installed-v1","project_id":"installed","files":[{"hashes":{"sha1":"11f6ad8ec52a2984abaafd7c3b516503785c2072"}}]}`
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw)), Request: r}, nil
		}
		return base.RoundTrip(r)
	})
	config := filepath.Join(t.TempDir(), "mcupdater.json")
	err := Run(context.Background(), []string{"init", "-config", config, "-server-dir", root, "-loader", "vanilla"}, strings.NewReader(""), io.Discard, io.Discard, client)
	if err == nil {
		t.Fatal("init accepted an installed mod for vanilla")
	}
	if _, err := os.Stat(config); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("init wrote an unsupported vanilla configuration: %v", err)
	}
	assertFile(t, root, "mods/installed.jar", "x")
}

func TestUntrackedModBlocksInsteadOfBeingDeleted(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "mods"), 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "mods", "unknown.jar")
	if err := os.WriteFile(path, []byte("untracked"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := inventory(root, Config{}, State{}); err == nil {
		t.Fatal("untracked installed mod was ignored")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "untracked" {
		t.Fatalf("untracked mod changed: %q %v", data, err)
	}
}

func TestUpdaterLockExcludesConcurrentWriters(t *testing.T) {
	root := t.TempDir()
	release, err := Lock(root)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Lock(root); err == nil {
		second()
		t.Fatal("second updater acquired lock")
	}
	release()
	next, err := Lock(root)
	if err != nil {
		t.Fatalf("released lock remains held: %v", err)
	}
	next()
}

func TestSafePathUsesCanonicalRelativePaths(t *testing.T) {
	root := t.TempDir()
	putFixture(t, root, "libraries/runtime/server.jar", "server")
	path, err := safePath(root, "libraries/runtime/server.jar")
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(root, "libraries", "runtime", "server.jar") {
		t.Fatalf("managed path resolved outside its native location: %q", path)
	}
	for _, rel := range []string{"../outside.jar", "mods/../server.jar", "/server.jar", "mods//mod.jar", `mods\mod.jar`, "mod\x00.jar"} {
		if _, err := safePath(root, rel); err == nil {
			t.Errorf("accepted noncanonical or unsafe path %q", rel)
		}
	}
}

func TestFetchVerified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("artifact")) }))
	defer server.Close()
	sum := sha256.Sum256([]byte("artifact"))
	good := Artifact{URL: server.URL, HashAlgorithm: "sha256", Hash: hex.EncodeToString(sum[:])}
	client := NewClient("")
	if data, err := client.fetchVerified(context.Background(), good); err != nil || string(data) != "artifact" {
		t.Fatalf("verified fetch failed: %q %v", data, err)
	}
	bad := good
	bad.Hash = strings.Repeat("0", 64)
	if _, err := client.fetchVerified(context.Background(), bad); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	if _, err := client.fetchVerified(context.Background(), Artifact{URL: server.URL}); err == nil {
		t.Fatal("unchecksummed artifact trusted")
	}
	wrongSize := good
	wrongSize.Size = 3
	if _, err := client.fetchVerified(context.Background(), wrongSize); err == nil {
		t.Fatal("size mismatch accepted")
	}
}
