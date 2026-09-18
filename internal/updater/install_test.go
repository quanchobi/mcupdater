package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func putFixture(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
func assertFile(t *testing.T, root, name, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, name))
	if err != nil || string(got) != want {
		t.Fatalf("%s: got %q, %v; want %q", name, got, err, want)
	}
}

func TestCommitFailureRestoresReplacedAndRemovedFiles(t *testing.T) {
	root, stage := t.TempDir(), t.TempDir()
	putFixture(t, root, "mods/a.jar", "old required")
	putFixture(t, root, "mods/b.jar", "old optional")
	putFixture(t, root, ".mcupdater/state.json", "old state")
	putFixture(t, stage, "mods/a.jar", "new required")
	putFixture(t, stage, ".mcupdater/state.json", "new state")
	// A staged file disappearing before its rename fails after prior operations.
	_, err := commitStage(context.Background(), root, stage, map[string]bool{"mods/a.jar": true, "mods/b.jar": false, "mods/z.jar": true, ".mcupdater/state.json": true})
	if err == nil {
		t.Fatal("missing staged file did not abort the transaction")
	}
	assertFile(t, root, "mods/a.jar", "old required")
	assertFile(t, root, "mods/b.jar", "old optional")
	assertFile(t, root, ".mcupdater/state.json", "old state")
	if _, err = os.Stat(filepath.Join(root, "mods/z.jar")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected new file: %v", err)
	}
}

func TestCommitBacksUpRemovedModsAndPreservesWorld(t *testing.T) {
	root, stage := t.TempDir(), t.TempDir()
	putFixture(t, root, "mods/optional.jar", "old optional")
	putFixture(t, root, "world/level.dat", "world data")
	putFixture(t, root, ".mcupdater/state.json", "old state")
	putFixture(t, stage, "mods/required.jar", "new required")
	putFixture(t, stage, ".mcupdater/state.json", "new state")
	backup, err := commitStage(context.Background(), root, stage, map[string]bool{"mods/optional.jar": false, "mods/required.jar": true, ".mcupdater/state.json": true})
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, root, "mods/required.jar", "new required")
	assertFile(t, root, ".mcupdater/state.json", "new state")
	assertFile(t, root, "world/level.dat", "world data")
	assertFile(t, backup, "old/mods/optional.jar", "old optional")
	assertFile(t, backup, "old/.mcupdater/state.json", "old state")
	if _, err = os.Stat(filepath.Join(root, "mods/optional.jar")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incompatible optional mod remains installed: %v", err)
	}
}

func TestVanillaUpdateDoesNotRequireJava(t *testing.T) {
	root := t.TempDir()
	putFixture(t, root, "server.jar", "old server")
	putFixture(t, root, "world/level.dat", "world data")
	putFixture(t, root, "eula.txt", "eula=false\n")
	config := filepath.Join(t.TempDir(), "mcupdater.json")
	client := plannerFixture(t, nil, 0)
	base := client.HTTP.Transport
	server := "verified vanilla server"
	metadata, err := json.Marshal(map[string]any{
		"id": "1.21.1", "javaVersion": map[string]int{"majorVersion": 21},
		"downloads": map[string]any{"server": map[string]any{"url": "https://fixture.invalid/server.jar", "sha1": "860fb78a01745fdfa2ed3efb272eec51adb090ba", "size": len(server)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	metadataText := string(metadata)
	client.HTTP.Transport = planTransport(func(r *http.Request) (*http.Response, error) {
		var raw string
		switch r.URL.String() {
		case minecraftManifestURL:
			return base.RoundTrip(r)
		case "https://fixture.invalid/game":
			raw = metadataText
		case "https://fixture.invalid/server.jar":
			raw = server
		default:
			return nil, fmt.Errorf("service unavailable: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw)), ContentLength: int64(len(raw)), Request: r}, nil
	})
	ctx := context.Background()
	if err := Run(ctx, []string{"init", "-config", config, "-server-dir", root, "-loader", "vanilla", "-java", filepath.Join(root, "missing-java")}, strings.NewReader(""), io.Discard, io.Discard, client); err != nil {
		t.Fatal(err)
	}
	if err := Run(ctx, []string{"update", "-config", config}, strings.NewReader("y\n"), io.Discard, io.Discard, client); err != nil {
		t.Fatal(err)
	}
	assertFile(t, root, "server.jar", server)
	assertFile(t, root, "world/level.dat", "world data")
	assertFile(t, root, "eula.txt", "eula=false\n")
	backups, err := os.ReadDir(filepath.Join(root, ".mcupdater", "backups"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected one recovery backup: %v, %v", backups, err)
	}
	assertFile(t, filepath.Join(root, ".mcupdater", "backups", backups[0].Name()), "old/server.jar", "old server")
	cfg, err := LoadConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := client.BuildPlan(ctx, cfg)
	if err != nil || plan.Changed {
		t.Fatalf("vanilla update is not idempotent: changed=%t, %v", plan.Changed, err)
	}
}
