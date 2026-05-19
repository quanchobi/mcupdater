package backup

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateBackup_CreatesZip(t *testing.T) {
	modsDir := t.TempDir()
	backupDir := t.TempDir()

	createTestJar(modsDir, "mod-a.jar", "content-a")
	createTestJar(modsDir, "mod-b.jar", "content-b")

	backupPath, err := CreateBackup(modsDir, backupDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if backupPath == "" {
		t.Fatal("expected non-empty backup path")
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 backup file, got %d", len(entries))
	}

	r, err := zip.OpenReader(backupPath)
	if err != nil {
		t.Fatalf("failed to open zip: %v", err)
	}
	defer r.Close()

	if len(r.File) != 2 {
		t.Fatalf("expected 2 files in zip, got %d", len(r.File))
	}

	names := map[string]bool{}
	for _, f := range r.File {
		names[f.Name] = true
	}
	if !names["mod-a.jar"] || !names["mod-b.jar"] {
		t.Errorf("zip missing expected files, got %v", names)
	}
}

func TestCreateBackup_SkipsNonJarFiles(t *testing.T) {
	modsDir := t.TempDir()
	backupDir := t.TempDir()

	createTestJar(modsDir, "mod.jar", "content")
	os.WriteFile(filepath.Join(modsDir, "readme.txt"), []byte("not a jar"), 0644)

	backupPath, err := CreateBackup(modsDir, backupDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	r, err := zip.OpenReader(backupPath)
	if err != nil {
		t.Fatalf("failed to open zip: %v", err)
	}
	defer r.Close()

	if len(r.File) != 1 {
		t.Errorf("expected 1 file in zip, got %d", len(r.File))
	}
	if r.File[0].Name != "mod.jar" {
		t.Errorf("expected mod.jar, got %s", r.File[0].Name)
	}
}

func TestCreateBackup_SkipsDirectories(t *testing.T) {
	modsDir := t.TempDir()
	backupDir := t.TempDir()

	createTestJar(modsDir, "mod.jar", "content")
	os.Mkdir(filepath.Join(modsDir, "subdir"), 0755)

	backupPath, err := CreateBackup(modsDir, backupDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	r, err := zip.OpenReader(backupPath)
	if err != nil {
		t.Fatalf("failed to open zip: %v", err)
	}
	defer r.Close()

	if len(r.File) != 1 {
		t.Errorf("expected 1 file in zip, got %d", len(r.File))
	}
}

func TestCreateBackup_EmptyModsDir(t *testing.T) {
	modsDir := t.TempDir()
	backupDir := t.TempDir()

	backupPath, err := CreateBackup(modsDir, backupDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if backupPath == "" {
		t.Fatal("expected non-empty backup path")
	}

	r, err := zip.OpenReader(backupPath)
	if err != nil {
		t.Fatalf("failed to open zip: %v", err)
	}
	defer r.Close()

	if len(r.File) != 0 {
		t.Errorf("expected 0 files in zip, got %d", len(r.File))
	}
}

func TestRestoreBackup_ExtractsZip(t *testing.T) {
	modsDir := t.TempDir()
	backupDir := t.TempDir()

	createTestJar(modsDir, "original.jar", "original")
	_, err := CreateBackup(modsDir, backupDir)
	if err != nil {
		t.Fatal(err)
	}

	os.Remove(filepath.Join(modsDir, "original.jar"))

	err = RestoreBackup(backupDir, modsDir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(modsDir, "original.jar"))
	if err != nil {
		t.Fatalf("failed to read restored file: %v", err)
	}
	if string(content) != "original" {
		t.Errorf("content = %q, want %q", string(content), "original")
	}
}

func TestRestoreBackup_ClearsExistingJars(t *testing.T) {
	modsDir := t.TempDir()
	backupDir := t.TempDir()

	createTestJar(modsDir, "old.jar", "old-content")
	createTestJar(modsDir, "old2.jar", "old-content-2")

	_, err := CreateBackup(modsDir, backupDir)
	if err != nil {
		t.Fatal(err)
	}

	os.Remove(filepath.Join(modsDir, "old.jar"))
	os.Remove(filepath.Join(modsDir, "old2.jar"))
	createTestJar(modsDir, "stale.jar", "stale")

	err = RestoreBackup(backupDir, modsDir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(modsDir, "stale.jar")); !os.IsNotExist(err) {
		t.Error("stale.jar should have been removed")
	}

	if _, err := os.Stat(filepath.Join(modsDir, "old.jar")); os.IsNotExist(err) {
		t.Error("old.jar should have been restored")
	}
}

func TestRestoreBackup_PreservesNonJarFiles(t *testing.T) {
	modsDir := t.TempDir()
	backupDir := t.TempDir()

	createTestJar(modsDir, "mod.jar", "content")
	os.WriteFile(filepath.Join(modsDir, "config.txt"), []byte("keep me"), 0644)

	_, err := CreateBackup(modsDir, backupDir)
	if err != nil {
		t.Fatal(err)
	}

	os.Remove(filepath.Join(modsDir, "mod.jar"))

	err = RestoreBackup(backupDir, modsDir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(modsDir, "config.txt"))
	if err != nil {
		t.Fatalf("config.txt should still exist: %v", err)
	}
	if string(content) != "keep me" {
		t.Errorf("config.txt content = %q, want %q", string(content), "keep me")
	}
}

func TestListBackups_SortedNewestFirst(t *testing.T) {
	backupDir := t.TempDir()

	os.WriteFile(filepath.Join(backupDir, "backup_2025-01-01_120000.zip"), []byte{}, 0644)
	os.WriteFile(filepath.Join(backupDir, "backup_2025-06-15_080000.zip"), []byte{}, 0644)
	os.WriteFile(filepath.Join(backupDir, "backup_2025-03-20_150000.zip"), []byte{}, 0644)

	backups, err := ListBackups(backupDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(backups) != 3 {
		t.Fatalf("expected 3 backups, got %d", len(backups))
	}

	if backups[0] != "backup_2025-06-15_080000.zip" {
		t.Errorf("first backup = %q, want newest", backups[0])
	}
	if backups[2] != "backup_2025-01-01_120000.zip" {
		t.Errorf("last backup = %q, want oldest", backups[2])
	}
}

func TestListBackups_SkipsNonBackupFiles(t *testing.T) {
	backupDir := t.TempDir()

	os.WriteFile(filepath.Join(backupDir, "backup_2025-01-01_120000.zip"), []byte{}, 0644)
	os.WriteFile(filepath.Join(backupDir, "random.txt"), []byte{}, 0644)
	os.WriteFile(filepath.Join(backupDir, "not-a-backup.zip"), []byte{}, 0644)

	backups, err := ListBackups(backupDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(backups) != 1 {
		t.Errorf("expected 1 backup, got %d", len(backups))
	}
}

func TestListBackups_NonexistentDir(t *testing.T) {
	backups, err := ListBackups("/nonexistent/dir")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(backups) != 0 {
		t.Errorf("expected empty slice, got %d items", len(backups))
	}
}

func TestFindBackup_EmptyDatetimeReturnsLatest(t *testing.T) {
	backupDir := t.TempDir()

	os.WriteFile(filepath.Join(backupDir, "backup_2025-01-01_120000.zip"), []byte{}, 0644)
	os.WriteFile(filepath.Join(backupDir, "backup_2025-06-15_080000.zip"), []byte{}, 0644)

	path, err := findBackup(backupDir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := filepath.Join(backupDir, "backup_2025-06-15_080000.zip")
	if path != expected {
		t.Errorf("path = %q, want %q", path, expected)
	}
}

func TestFindBackup_DatetimeMatch(t *testing.T) {
	backupDir := t.TempDir()

	os.WriteFile(filepath.Join(backupDir, "backup_2025-01-01_120000.zip"), []byte{}, 0644)
	os.WriteFile(filepath.Join(backupDir, "backup_2025-06-15_080000.zip"), []byte{}, 0644)

	path, err := findBackup(backupDir, "2025-01-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := filepath.Join(backupDir, "backup_2025-01-01_120000.zip")
	if path != expected {
		t.Errorf("path = %q, want %q", path, expected)
	}
}

func TestFindBackup_NoMatch(t *testing.T) {
	backupDir := t.TempDir()

	os.WriteFile(filepath.Join(backupDir, "backup_2025-01-01_120000.zip"), []byte{}, 0644)

	_, err := findBackup(backupDir, "9999-99-99")
	if err == nil {
		t.Fatal("expected error for no match, got nil")
	}
}

func createTestJar(dir, name, content string) {
	os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)
}
