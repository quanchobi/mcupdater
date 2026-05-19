package api

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/quanchobi/mcupdater/internal/logger"
)

func TestSelectVersionAndFile_EmptyVersions(t *testing.T) {
	_, _, err := selectVersionAndFile([]ModInfo{}, "")
	if err == nil {
		t.Fatal("expected error for empty versions, got nil")
	}
}

func TestSelectVersionAndFile_LatestWhenNoVersion(t *testing.T) {
	versions := []ModInfo{
		{VersionNumber: "1.0.0", DatePublished: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Files: []ModFile{{Filename: "mod-1.0.0.jar", URL: "http://example.com/1.0.0"}}},
		{VersionNumber: "2.0.0", DatePublished: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC), Files: []ModFile{{Filename: "mod-2.0.0.jar", URL: "http://example.com/2.0.0"}}},
		{VersionNumber: "1.5.0", DatePublished: time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC), Files: []ModFile{{Filename: "mod-1.5.0.jar", URL: "http://example.com/1.5.0"}}},
	}

	target, file, err := selectVersionAndFile(versions, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if target.VersionNumber != "2.0.0" {
		t.Errorf("version = %q, want 2.0.0 (latest by date)", target.VersionNumber)
	}
	if file.Filename != "mod-2.0.0.jar" {
		t.Errorf("filename = %q, want mod-2.0.0.jar", file.Filename)
	}
}

func TestSelectVersionAndFile_SpecificVersion(t *testing.T) {
	versions := []ModInfo{
		{VersionNumber: "1.0.0", DatePublished: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC), Files: []ModFile{{Filename: "mod-1.0.0.jar", URL: "http://example.com/1.0.0"}}},
		{VersionNumber: "2.0.0", DatePublished: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Files: []ModFile{{Filename: "mod-2.0.0.jar", URL: "http://example.com/2.0.0"}}},
	}

	target, file, err := selectVersionAndFile(versions, "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if target.VersionNumber != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", target.VersionNumber)
	}
	if file.Filename != "mod-1.0.0.jar" {
		t.Errorf("filename = %q, want mod-1.0.0.jar", file.Filename)
	}
}

func TestSelectVersionAndFile_VersionNotFound(t *testing.T) {
	versions := []ModInfo{
		{VersionNumber: "1.0.0", DatePublished: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Files: []ModFile{{Filename: "mod-1.0.0.jar"}}},
	}

	_, _, err := selectVersionAndFile(versions, "9.9.9")
	if err == nil {
		t.Fatal("expected error for version not found, got nil")
	}
	if !strings.Contains(err.Error(), "9.9.9") {
		t.Errorf("error should mention version, got: %v", err)
	}
}

func TestSelectVersionAndFile_NoFiles(t *testing.T) {
	versions := []ModInfo{
		{VersionNumber: "1.0.0", DatePublished: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Files: []ModFile{}},
	}

	_, _, err := selectVersionAndFile(versions, "")
	if err == nil {
		t.Fatal("expected error for no files, got nil")
	}
}

func TestSelectVersionAndFile_PrimaryFileSelection(t *testing.T) {
	versions := []ModInfo{
		{
			VersionNumber:   "1.0.0",
			DatePublished:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			Files: []ModFile{
				{Filename: "mod-1.0.0.jar", URL: "http://example.com/normal", Primary: false},
				{Filename: "mod-1.0.0-primary.jar", URL: "http://example.com/primary", Primary: true},
			},
		},
	}

	_, file, err := selectVersionAndFile(versions, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if file.Filename != "mod-1.0.0-primary.jar" {
		t.Errorf("filename = %q, want primary file", file.Filename)
	}
	if file.URL != "http://example.com/primary" {
		t.Errorf("url = %q, want primary url", file.URL)
	}
}

func TestSelectVersionAndFile_FirstFileWhenNoPrimary(t *testing.T) {
	versions := []ModInfo{
		{
			VersionNumber:   "1.0.0",
			DatePublished:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			Files: []ModFile{
				{Filename: "mod-1.0.0.jar", URL: "http://example.com/first", Primary: false},
				{Filename: "mod-1.0.0-alt.jar", URL: "http://example.com/alt", Primary: false},
			},
		},
	}

	_, file, err := selectVersionAndFile(versions, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if file.Filename != "mod-1.0.0.jar" {
		t.Errorf("filename = %q, want first file", file.Filename)
	}
}

func TestValidateMinecraftVersion_Valid(t *testing.T) {
	valid := []string{"1.21.1", "1.20", "1.16.5", "26.1.0"}
	for _, v := range valid {
		err := validateMinecraftVersion(v)
		if err != nil {
			t.Errorf("version %q should be valid, got error: %v", v, err)
		}
	}
}

func TestValidateMinecraftVersion_Invalid(t *testing.T) {
	invalid := []string{"1", "abc", ""}
	for _, v := range invalid {
		err := validateMinecraftVersion(v)
		if err == nil {
			t.Errorf("version %q should be invalid, got nil error", v)
		}
	}
}

func TestIsFabricCompatible(t *testing.T) {
	compatible := []string{"1.10", "1.13", "1.14", "1.16.5", "1.21.1", "26.1.0", "27.0.0"}
	for _, v := range compatible {
		if !isFabricCompatible(v) {
			t.Errorf("version %q should be fabric compatible", v)
		}
	}

	incompatible := []string{"2.0.0", "25.0.0", "a1.0.0", "b.1.2"}
	for _, v := range incompatible {
		if isFabricCompatible(v) {
			t.Errorf("version %q should NOT be fabric compatible", v)
		}
	}
}

func TestIsNeoForgeCompatible(t *testing.T) {
	compatible := []string{"1.20.2", "1.20.3", "1.21.1", "1.22.0"}
	for _, v := range compatible {
		if !isNeoForgeCompatible(v) {
			t.Errorf("version %q should be neoforge compatible", v)
		}
	}

	incompatible := []string{"1.20.1", "1.20.0", "1.19.4", "1.16.5", "26.1.0"}
	for _, v := range incompatible {
		if isNeoForgeCompatible(v) {
			t.Errorf("version %q should NOT be neoforge compatible", v)
		}
	}
}

func TestSelectNeoForgeVersion(t *testing.T) {
	versions := []string{"20.1.0", "20.2.0", "20.2.1", "21.0.0", "21.1.0"}

	result := selectNeoForgeVersion(versions, "21")
	if result != "21.1.0" {
		t.Errorf("result = %q, want 21.1.0 (latest 21.x)", result)
	}

	result = selectNeoForgeVersion(versions, "20")
	if result != "20.2.1" {
		t.Errorf("result = %q, want 20.2.1 (latest 20.x)", result)
	}

	result = selectNeoForgeVersion(versions, "22")
	if result != "" {
		t.Errorf("result = %q, want empty (no 22.x versions)", result)
	}
}

func TestForgePromoKey(t *testing.T) {
	result := forgePromoKey("1.21.1")
	expected := "1.21.1-recommended"
	if result != expected {
		t.Errorf("result = %q, want %q", result, expected)
	}
}

func TestModChange_IsNew(t *testing.T) {
	existingMods := map[string]string{}
	change := determineChange("fabric-api", "fabric-api-0.100.0.jar", existingMods)

	if !change.IsNew {
		t.Error("expected IsNew = true")
	}
	if change.IsUpdate {
		t.Error("expected IsUpdate = false")
	}
}

func TestModChange_IsUpdate(t *testing.T) {
	existingMods := map[string]string{
		"fabric-api-0.100.0.jar": "fabric-api-0.100.0.jar",
	}
	change := determineChange("fabric-api", "fabric-api-0.100.0.jar", existingMods)

	if change.IsNew {
		t.Error("expected IsNew = false")
	}
	if !change.IsUpdate {
		t.Error("expected IsUpdate = true")
	}
	if change.OldVersion != "fabric-api-0.100.0.jar" {
		t.Errorf("OldVersion = %q, want fabric-api-0.100.0.jar", change.OldVersion)
	}
}

func determineChange(name, filename string, existingMods map[string]string) ModChange {
	var change ModChange
	change.Name = name
	change.NewVersion = name

	existingFile, exists := existingMods[filename]
	if exists {
		change.OldVersion = existingFile
		change.IsUpdate = true
	} else {
		change.IsNew = true
	}
	return change
}

func TestLogger_WithWriter(t *testing.T) {
	var buf bytes.Buffer
	log := logger.NewWithWriter(&buf)

	log.Info("test message %d", 42)
	log.Warn("warning message")
	log.Error("error message")

	output := buf.String()

	if !strings.Contains(output, "[INFO]") {
		t.Error("output missing INFO level")
	}
	if !strings.Contains(output, "[WARN]") {
		t.Error("output missing WARN level")
	}
	if !strings.Contains(output, "[ERROR]") {
		t.Error("output missing ERROR level")
	}
	if !strings.Contains(output, "test message 42") {
		t.Error("output missing formatted message")
	}
	if !strings.Contains(output, "warning message") {
		t.Error("output missing warning message")
	}
	if !strings.Contains(output, "error message") {
		t.Error("output missing error message")
	}
}

func TestLogger_CloseNoPanic(t *testing.T) {
	var buf bytes.Buffer
	log := logger.NewWithWriter(&buf)

	log.Close()
	log.Close()
}
