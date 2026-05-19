package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/quanchobi/mcupdater/internal/config"
)

func TestDownloadFile_Success(t *testing.T) {
	content := "test file content"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(content))
	}))
	defer server.Close()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "downloaded.txt")

	err := downloadFile(server.Client(), filePath, server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(data) != content {
		t.Errorf("content = %q, want %q", string(data), content)
	}
}

func TestDownloadFile_Non200Status(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "downloaded.txt")

	err := downloadFile(server.Client(), filePath, server.URL)
	if err == nil {
		t.Fatal("expected error for non-200 status, got nil")
	}
}

func TestDownloadFile_InvalidURL(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "downloaded.txt")

	err := downloadFile(http.DefaultClient, filePath, "http://localhost:1")
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}

func TestModInfo_JSONParsing(t *testing.T) {
	response := `[{"game_versions":["1.21.1"],"loaders":["fabric"],"id":"abc123","project_id":"proj1","author_id":"auth1","featured":false,"name":"Test Mod","version_number":"1.0.0","changelog":"","changelog_url":null,"date_published":"2025-01-01T00:00:00Z","downloads":100,"version_type":"release","status":"listed","requested_status":null,"files":[{"id":"file1","hashes":{"sha1":"abc","sha512":"def"},"url":"http://example.com/mod.jar","filename":"mod.jar","primary":true,"size":1000,"file_type":null}],"dependencies":[]}]`

	var mods []ModInfo
	err := json.Unmarshal([]byte(response), &mods)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mods) != 1 {
		t.Fatalf("expected 1 mod, got %d", len(mods))
	}

	if mods[0].VersionNumber != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", mods[0].VersionNumber)
	}
	if mods[0].Files[0].Filename != "mod.jar" {
		t.Errorf("filename = %q, want mod.jar", mods[0].Files[0].Filename)
	}
	if !mods[0].Files[0].Primary {
		t.Error("expected primary file")
	}
}

func TestModInfo_EmptyJSON(t *testing.T) {
	var mods []ModInfo
	err := json.Unmarshal([]byte("[]"), &mods)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mods) != 0 {
		t.Errorf("expected 0 mods, got %d", len(mods))
	}
}

func TestGetFabricLoader_ExplicitVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake jar content"))
	}))
	defer server.Close()

	cfg := config.Config{}
	cfg.Minecraft.Version = "1.21.1"
	cfg.Loader.Name = "fabric"
	cfg.Loader.Version = "0.15.0"

	err := getFabricLoader(server.Client(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
