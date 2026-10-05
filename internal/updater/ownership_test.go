package updater

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceUnmanagedValidation(t *testing.T) {
	base := Config{Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "latest"}}
	for _, ok := range [][]string{{"server.jar"}, {"libraries/"}, {"run.sh", "libraries/net/x.jar"}} {
		cfg := base
		cfg.ReplaceUnmanaged = ok
		if err := ValidateConfig(cfg); err != nil {
			t.Errorf("%v rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/", "/etc/passwd", "../x.jar", "a//b", `a\b`, ".mcupdater/state.json",
		"mods/x.jar", "mods/", "world/", "config/", "eula.txt", "server.properties"} {
		cfg := base
		cfg.ReplaceUnmanaged = []string{bad}
		if err := ValidateConfig(cfg); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCoveredByReplace(t *testing.T) {
	list := []string{"server.jar", "libraries/"}
	for path, want := range map[string]bool{"server.jar": true, "libraries/a/b.jar": true,
		"librariesX/a.jar": false, "server.jar.bak": false, "run.sh": false} {
		if got := coveredByReplace(list, path); got != want {
			t.Errorf("%s: got %v want %v", path, got, want)
		}
	}
}

// setReplaceUnmanaged rewrites a config file in place (WriteConfig is O_EXCL).
func setReplaceUnmanaged(t *testing.T, config string, paths ...string) {
	t.Helper()
	editConfig(t, config, func(cfg *Config) { cfg.ReplaceUnmanaged = paths })
}

func editConfig(t *testing.T, config string, edit func(*Config)) {
	t.Helper()
	cfg, err := LoadConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	edit(&cfg)
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func assertNoBackups(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, ".mcupdater", "backups")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused operation created a backup: %v", err)
	}
}

func TestUpdateRefusesUnmanagedServerJar(t *testing.T) {
	root := t.TempDir()
	putFixture(t, root, "server.jar", "someone else's launcher")
	config := filepath.Join(t.TempDir(), "mcupdater.json")
	client := vanillaClient(t)
	ctx := context.Background()
	if err := Run(ctx, []string{"init", "-config", config, "-server-dir", root, "-loader", "vanilla"}, strings.NewReader(""), io.Discard, io.Discard, client); err != nil {
		t.Fatal(err)
	}
	err := Run(ctx, []string{"update", "-config", config}, strings.NewReader("y\n"), io.Discard, io.Discard, client)
	if err == nil || !strings.Contains(err.Error(), "replace_unmanaged") || !strings.Contains(err.Error(), "server.jar") {
		t.Fatalf("expected ownership refusal naming server.jar, got %v", err)
	}
	assertFile(t, root, "server.jar", "someone else's launcher")
	assertNoBackups(t, root)

	setReplaceUnmanaged(t, config, "server.jar")
	if err := Run(ctx, []string{"update", "-config", config}, strings.NewReader("y\n"), io.Discard, io.Discard, client); err != nil {
		t.Fatal(err)
	}
	assertFile(t, root, "server.jar", vanillaServerBytes)
}

func TestCheckReportsUnmanagedServerJar(t *testing.T) {
	root := t.TempDir()
	putFixture(t, root, "server.jar", "someone else's launcher")
	config := filepath.Join(t.TempDir(), "mcupdater.json")
	client := vanillaClient(t)
	ctx := context.Background()
	if err := Run(ctx, []string{"init", "-config", config, "-server-dir", root, "-loader", "vanilla"}, strings.NewReader(""), io.Discard, io.Discard, client); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	err := Run(ctx, []string{"check", "-config", config}, strings.NewReader(""), &out, io.Discard, client)
	if err == nil || !strings.Contains(err.Error(), "replace_unmanaged") || !strings.Contains(err.Error(), "server.jar") {
		t.Fatalf("check did not report the collision: %v", err)
	}
	if strings.Contains(out.String(), "Already up to date") {
		t.Fatal("check claimed up to date despite a collision")
	}
}

func TestOwnershipAllowsManagedAndAbsentPaths(t *testing.T) {
	root := t.TempDir()
	putFixture(t, root, "managed.jar", "ours")
	putFixture(t, root, "libraries/x/y.jar", "unmanaged library")
	cfg := Config{ServerDir: root}
	p := Plan{Previous: State{Files: map[string]string{"managed.jar": "digest"}}}
	if err := checkOwnership(cfg, p, map[string]string{"managed.jar": "", "absent.jar": ""}); err != nil {
		t.Fatalf("managed or absent paths refused: %v", err)
	}
	err := checkOwnership(cfg, p, map[string]string{"libraries/x/y.jar": ""})
	if err == nil || !strings.Contains(err.Error(), "libraries/x/y.jar") {
		t.Fatalf("unmanaged library accepted: %v", err)
	}
	cfg.ReplaceUnmanaged = []string{"libraries/"}
	if err := checkOwnership(cfg, p, map[string]string{"libraries/x/y.jar": ""}); err != nil {
		t.Fatalf("acknowledged directory refused: %v", err)
	}
}
