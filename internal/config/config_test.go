package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadConfig_ValidTOML(t *testing.T) {
	content := `[config]
backup_path = "/tmp/backup"
log_path = "/tmp/log"
mods_path = "./mods"

[minecraft]
version = "1.21.1"

[loader]
name = "fabric"
version = "0.15.0"

[[mods.required]]
name = "fabric-api"
version = "0.100.0"

[[mods.optional]]
name = "lithium"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := os.WriteFile(path, []byte(content), 0644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Config.BackupPath != "/tmp/backup" {
		t.Errorf("backup_path = %q, want %q", cfg.Config.BackupPath, "/tmp/backup")
	}
	if cfg.Minecraft.Version != "1.21.1" {
		t.Errorf("version = %q, want %q", cfg.Minecraft.Version, "1.21.1")
	}
	if cfg.Loader.Name != "fabric" {
		t.Errorf("loader name = %q, want %q", cfg.Loader.Name, "fabric")
	}
	if cfg.Loader.Version != "0.15.0" {
		t.Errorf("loader version = %q, want %q", cfg.Loader.Version, "0.15.0")
	}
	if len(cfg.Mods.Required) != 1 {
		t.Fatalf("required mods count = %d, want 1", len(cfg.Mods.Required))
	}
	if cfg.Mods.Required[0].Name != "fabric-api" {
		t.Errorf("required mod name = %q, want %q", cfg.Mods.Required[0].Name, "fabric-api")
	}
	if cfg.Mods.Required[0].Version != "0.100.0" {
		t.Errorf("required mod version = %q, want %q", cfg.Mods.Required[0].Version, "0.100.0")
	}
	if len(cfg.Mods.Optional) != 1 {
		t.Fatalf("optional mods count = %d, want 1", len(cfg.Mods.Optional))
	}
	if cfg.Mods.Optional[0].Name != "lithium" {
		t.Errorf("optional mod name = %q, want %q", cfg.Mods.Optional[0].Name, "lithium")
	}
	if cfg.Mods.Optional[0].Version != "" {
		t.Errorf("optional mod version = %q, want empty", cfg.Mods.Optional[0].Version)
	}
}

func TestReadConfig_Defaults(t *testing.T) {
	content := `[minecraft]
version = "1.21.1"

[loader]
name = "fabric"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := os.WriteFile(path, []byte(content), 0644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Config.BackupPath != "/var/lib/mcupdater/backup" {
		t.Errorf("backup_path default = %q, want %q", cfg.Config.BackupPath, "/var/lib/mcupdater/backup")
	}
	if cfg.Config.LogPath != "/var/log/mcupdater.log" {
		t.Errorf("log_path default = %q, want %q", cfg.Config.LogPath, "/var/log/mcupdater.log")
	}
	if cfg.Config.ModsPath != "./mods" {
		t.Errorf("mods_path default = %q, want %q", cfg.Config.ModsPath, "./mods")
	}
}

func TestReadConfig_InvalidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := os.WriteFile(path, []byte("[invalid\nnot valid toml"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	_, err = ReadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid TOML, got nil")
	}
}

func TestReadConfig_NonexistentFile(t *testing.T) {
	_, err := ReadConfig("/nonexistent/path/config.toml")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestFindConfig_ExplicitPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := os.WriteFile(path, []byte("[minecraft]\nversion = \"1.21.1\""), 0644)
	if err != nil {
		t.Fatal(err)
	}

	found, err := FindConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found != path {
		t.Errorf("found = %q, want %q", found, path)
	}
}

func TestFindConfig_ExplicitPathNotFound(t *testing.T) {
	_, err := FindConfig("/nonexistent/config.toml")
	if err == nil {
		t.Fatal("expected error for nonexistent explicit path, got nil")
	}
}

func TestFindConfig_CurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	err := os.WriteFile(path, []byte("[minecraft]\nversion = \"1.21.1\""), 0644)
	if err != nil {
		t.Fatal(err)
	}

	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	found, err := FindConfig("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found != "./config.toml" {
		t.Errorf("found = %q, want %q", found, "./config.toml")
	}
}

func TestFindConfig_NoConfigFound(t *testing.T) {
	dir := t.TempDir()
	origWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origWd)

	_, err := FindConfig("")
	if err != os.ErrNotExist {
		t.Errorf("expected os.ErrNotExist, got %v", err)
	}
}
