package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

// safePath rejects symlink traversal and non-regular file endpoints. The server
// must be stopped and its directory must not be concurrently modified.
// Managed relative paths use forward slashes on every platform.
func safePath(root, rel string) (string, error) {
	if !filepath.IsLocal(rel) || strings.ContainsAny(rel, "\\\x00") || filepath.ToSlash(filepath.Clean(rel)) != rel {
		return "", fmt.Errorf("unsafe relative path %q", rel)
	}
	current := filepath.Clean(root)
	// Check root ancestors as well: a mods or server directory symlink must not
	// redirect an update outside the selected tree.
	for p := current; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink path forbidden: %s", p)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	for _, part := range strings.Split(rel, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return "", fmt.Errorf("unsafe file type: %s", current)
		}
	}
	return current, nil
}

// Lock serializes updater processes. A stale lock after a crash must be removed
// manually only after confirming no updater is still running.
func Lock(root string) (func(), error) {
	dir, err := safePath(root, ".mcupdater")
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	path, err := safePath(root, ".mcupdater/lock")
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("cannot acquire updater lock (if stale, remove %s after checking running processes): %w", path, err)
	}
	if _, err = fmt.Fprintf(f, "pid=%d\n", os.Getpid()); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		os.Remove(path)
		return nil, errors.Join(err, closeErr)
	}
	return func() { os.Remove(path) }, nil
}

type moveRecord struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
}

func (c *Client) Apply(ctx context.Context, cfg Config, p Plan, out io.Writer) (string, error) {
	if !p.Changed {
		return "", nil
	}
	stage, err := os.MkdirTemp(filepath.Join(cfg.ServerDir, ".mcupdater"), "stage-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	fmt.Fprintln(out, "Staging server and mods; live server files are unchanged...")
	start, err := c.InstallServer(ctx, p.Server, stage, cfg.Java, out)
	if err != nil {
		return "", fmt.Errorf("stage server: %w", err)
	}
	state := State{Minecraft: p.Server.Minecraft, Loader: p.Server.Loader, Mods: map[string]InstalledMod{}, Files: map[string]string{}, Start: start}
	if p.Server.Loader.Layout == "launcher" {
		state.Installer = p.Server.InstallerVersion
	}
	for _, pm := range p.Mods {
		if pm.Missing != "" {
			state.Mods[pm.Mod.Key()] = InstalledMod{}
			continue
		}
		name := pm.Release.Artifact.Filename
		if !safeJarName(name) {
			return "", fmt.Errorf("unsafe mod filename %q", name)
		}
		dest := filepath.Join(stage, "mods", name)
		fmt.Fprintf(out, "Downloading %s...\n", pm.Mod.Label())
		if err = c.Download(ctx, pm.Release.Artifact, dest); err != nil {
			return "", fmt.Errorf("download %s: %w", pm.Mod.Label(), err)
		}
		digest, err := fileHash(dest)
		if err != nil {
			return "", err
		}
		state.Mods[pm.Mod.Key()] = InstalledMod{File: name, Version: pm.Release.VersionID, SHA256: digest}
	}
	// JVM memory settings belong to the administrator, not the installer.
	userArgs, err := safePath(cfg.ServerDir, "user_jvm_args.txt")
	if err != nil {
		return "", err
	}
	if _, err = os.Stat(userArgs); err == nil {
		if err = os.Remove(filepath.Join(stage, "user_jvm_args.txt")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	err = filepath.WalkDir(stage, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if path == stage {
			return nil
		}
		rel, e := filepath.Rel(stage, path)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("installer created symlink: %s", rel)
		}
		if d.IsDir() {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("installer created non-regular file %s", rel)
		}
		first := strings.Split(rel, "/")[0]
		if first == ".mcupdater" || first == "world" || first == "config" || first == "eula.txt" || first == "server.properties" {
			return fmt.Errorf("installer produced protected path %s", rel)
		}
		if _, e = safePath(cfg.ServerDir, rel); e != nil {
			return e
		}
		digest, e := fileHash(path)
		if e != nil {
			return e
		}
		state.Files[rel] = digest
		return nil
	})
	if err != nil {
		return "", err
	}
	if err = checkOwnership(cfg, p, state.Files); err != nil {
		return "", err
	}
	current, err := inventory(cfg.ServerDir, cfg, p.Previous)
	if err != nil {
		return "", err
	}
	if !reflect.DeepEqual(current, p.Inventory) {
		return "", fmt.Errorf("mods changed after planning; retry update")
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Join(stage, ".mcupdater"), 0755); err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(stage, ".mcupdater", "state.json"), append(data, '\n'), 0644); err != nil {
		return "", err
	}
	replace := map[string]bool{".mcupdater/state.json": true}
	for name := range state.Files {
		replace[name] = true
	}
	for name := range p.Previous.Files {
		if name != "user_jvm_args.txt" {
			if _, ok := replace[name]; !ok {
				replace[name] = false
			}
		}
	}
	for name := range p.Inventory {
		rel := filepath.ToSlash(filepath.Join("mods", name))
		if _, ok := replace[rel]; !ok {
			replace[rel] = false
		}
	}
	backup, err := commitStage(ctx, cfg.ServerDir, stage, replace)
	if err != nil {
		return backup, err
	}
	fmt.Fprintf(out, "Update complete. Backup: %s\nStart from %s with: %s\n", backup, cfg.ServerDir, start)
	return backup, nil
}

func commitStage(ctx context.Context, root, stage string, replace map[string]bool) (string, error) {
	// State goes last: a failed commit must never advertise success.
	names := make([]string, 0, len(replace))
	for name := range replace {
		if name != ".mcupdater/state.json" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	names = append(names, ".mcupdater/state.json")
	for _, name := range names {
		path, e := safePath(root, name)
		if e != nil {
			return "", e
		}
		if info, e := os.Stat(path); e == nil && info.IsDir() {
			return "", fmt.Errorf("destination is a directory: %s", name)
		} else if e != nil && !errors.Is(e, os.ErrNotExist) {
			return "", e
		}
	}
	backupRoot, err := safePath(root, ".mcupdater/backups")
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(backupRoot, 0755); err != nil {
		return "", err
	}
	backup, err := os.MkdirTemp(backupRoot, time.Now().UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		return "", err
	}
	// Write the complete recovery manifest before the first mutation. old/ mirrors
	// relative paths; newly created paths are explicitly recorded as such.
	records := make([]moveRecord, 0, len(names))
	for _, name := range names {
		_, e := os.Stat(filepath.Join(root, name))
		records = append(records, moveRecord{Path: name, Existed: e == nil})
	}
	manifest, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(filepath.Join(backup, "manifest.json"), manifest, 0600); err != nil {
		return "", err
	}
	type appliedMove struct {
		name             string
		saved, installed bool
	}
	var applied []appliedMove
	rollback := func(cause error) (string, error) {
		var restoreErrors []error
		for i := len(applied) - 1; i >= 0; i-- {
			m := applied[i]
			dest := filepath.Join(root, m.name)
			if m.installed {
				if e := os.Remove(dest); e != nil && !errors.Is(e, os.ErrNotExist) {
					restoreErrors = append(restoreErrors, e)
				}
			}
			if m.saved {
				if e := os.Rename(filepath.Join(backup, "old", m.name), dest); e != nil {
					restoreErrors = append(restoreErrors, e)
				}
			}
		}
		if len(restoreErrors) > 0 {
			return backup, fmt.Errorf("update failed: %w; rollback incomplete: %v; recovery files: %s", cause, errors.Join(restoreErrors...), backup)
		}
		return backup, fmt.Errorf("update failed and was rolled back: %w", cause)
	}
	for _, name := range names {
		if err = ctx.Err(); err != nil {
			return rollback(err)
		}
		dest, e := safePath(root, name)
		if e != nil {
			return rollback(e)
		}
		applied = append(applied, appliedMove{name: name})
		m := &applied[len(applied)-1]
		if _, e = os.Stat(dest); e == nil {
			old := filepath.Join(backup, "old", name)
			if e = os.MkdirAll(filepath.Dir(old), 0755); e != nil {
				return rollback(e)
			}
			if e = os.Rename(dest, old); e != nil {
				return rollback(e)
			}
			m.saved = true
		} else if !errors.Is(e, os.ErrNotExist) {
			return rollback(e)
		}
		if replace[name] {
			if e = os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
				return rollback(e)
			}
			if e = os.Rename(filepath.Join(stage, name), dest); e != nil {
				return rollback(e)
			}
			m.installed = true
		}
	}
	return backup, nil
}
