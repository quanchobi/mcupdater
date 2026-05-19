package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/quanchobi/mcupdater/internal/api"
)

func TestContains_True(t *testing.T) {
	if !contains("backup_2025-01-15_143022.zip", "2025-01-15") {
		t.Error("expected contains to return true")
	}
}

func TestContains_False(t *testing.T) {
	if contains("backup_2025-01-15_143022.zip", "9999-99-99") {
		t.Error("expected contains to return false")
	}
}

func TestContains_EmptySubstring(t *testing.T) {
	if !contains("anything", "") {
		t.Error("expected contains to return true for empty substring")
	}
}

func TestPrintChanges_NewMods(t *testing.T) {
	var buf bytes.Buffer
	changes := []api.ModChange{
		{Name: "fabric-api", NewVersion: "0.100.0", IsNew: true},
		{Name: "lithium", NewVersion: "0.15.0", IsNew: true},
	}

	printChanges(&buf, changes)

	output := buf.String()
	if !strings.Contains(output, "[NEW]") {
		t.Error("output missing [NEW] marker")
	}
	if !strings.Contains(output, "fabric-api") {
		t.Error("output missing fabric-api")
	}
	if !strings.Contains(output, "lithium") {
		t.Error("output missing lithium")
	}
}

func TestPrintChanges_UpdateMods(t *testing.T) {
	var buf bytes.Buffer
	changes := []api.ModChange{
		{Name: "fabric-api", OldVersion: "fabric-api-0.99.0.jar", NewVersion: "0.100.0", IsUpdate: true},
	}

	printChanges(&buf, changes)

	output := buf.String()
	if !strings.Contains(output, "[UPDATE]") {
		t.Error("output missing [UPDATE] marker")
	}
	if !strings.Contains(output, "0.99.0") {
		t.Error("output missing old version")
	}
	if !strings.Contains(output, "0.100.0") {
		t.Error("output missing new version")
	}
}

func TestPrintChanges_Empty(t *testing.T) {
	var buf bytes.Buffer
	changes := []api.ModChange{}

	printChanges(&buf, changes)

	output := buf.String()
	if output != "\n" {
		t.Errorf("expected only newline, got %q", output)
	}
}

func TestConfirmPrompt_Yes(t *testing.T) {
	in := strings.NewReader("y\n")
	result := confirmPrompt(in)
	if !result {
		t.Error("expected true for 'y'")
	}
}

func TestConfirmPrompt_YesUpper(t *testing.T) {
	in := strings.NewReader("Y\n")
	result := confirmPrompt(in)
	if !result {
		t.Error("expected true for 'Y'")
	}
}

func TestConfirmPrompt_No(t *testing.T) {
	in := strings.NewReader("n\n")
	result := confirmPrompt(in)
	if result {
		t.Error("expected false for 'n'")
	}
}

func TestConfirmPrompt_Empty(t *testing.T) {
	in := strings.NewReader("\n")
	result := confirmPrompt(in)
	if result {
		t.Error("expected false for empty input")
	}
}
