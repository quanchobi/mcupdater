package backup

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func CreateBackup(modsPath, backupDir string) (string, error) {
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", err
	}

	timestamp := time.Now().UTC().Format("2006-01-02_150405")
	backupName := fmt.Sprintf("backup_%s.zip", timestamp)
	backupPath := filepath.Join(backupDir, backupName)

	zipFile, err := os.Create(backupPath)
	if err != nil {
		return "", err
	}
	defer zipFile.Close()

	w := zip.NewWriter(zipFile)
	defer w.Close()

	entries, err := os.ReadDir(modsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return backupPath, nil
		}
		return "", err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".jar") {
			continue
		}

		srcPath := filepath.Join(modsPath, entry.Name())
		f, err := os.Open(srcPath)
		if err != nil {
			return "", err
		}

		zipEntry, err := w.Create(entry.Name())
		if err != nil {
			f.Close()
			return "", err
		}

		_, err = io.Copy(zipEntry, f)
		f.Close()
		if err != nil {
			return "", err
		}
	}

	return backupPath, nil
}

func RestoreBackup(backupDir, modsPath string, datetime string) error {
	backupPath, err := findBackup(backupDir, datetime)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(modsPath, 0755); err != nil {
		return err
	}

	entries, err := os.ReadDir(modsPath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jar") {
			os.Remove(filepath.Join(modsPath, entry.Name()))
		}
	}

	r, err := zip.OpenReader(backupPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}

		dstPath := filepath.Join(modsPath, f.Name)
		dstFile, err := os.Create(dstPath)
		if err != nil {
			return err
		}

		srcFile, err := f.Open()
		if err != nil {
			dstFile.Close()
			return err
		}

		_, err = io.Copy(dstFile, srcFile)
		srcFile.Close()
		dstFile.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

func ListBackups(backupDir string) ([]string, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	var backups []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), "backup_") && strings.HasSuffix(entry.Name(), ".zip") {
			backups = append(backups, entry.Name())
		}
	}

	sort.Sort(sort.Reverse(sort.StringSlice(backups)))
	return backups, nil
}

func findBackup(backupDir, datetime string) (string, error) {
	if datetime == "" {
		backups, err := ListBackups(backupDir)
		if err != nil {
			return "", err
		}
		if len(backups) == 0 {
			return "", fmt.Errorf("no backups found in %s", backupDir)
		}
		return filepath.Join(backupDir, backups[0]), nil
	}

	backups, err := ListBackups(backupDir)
	if err != nil {
		return "", err
	}

	for _, name := range backups {
		if strings.Contains(name, datetime) {
			return filepath.Join(backupDir, name), nil
		}
	}

	return "", fmt.Errorf("no backup matching %q found in %s", datetime, backupDir)
}
