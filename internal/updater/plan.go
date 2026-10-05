package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type InstalledMod struct {
	File    string `json:"file"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}
type State struct {
	Minecraft string                  `json:"minecraft"`
	Loader    LoaderConfig            `json:"loader"`
	Mods      map[string]InstalledMod `json:"mods"`
	Files     map[string]string       `json:"files"`
	Start     string                  `json:"start"`
	Installer string                  `json:"installer,omitempty"`
}
type PlannedMod struct {
	Mod     Mod
	Release ModRelease
	Missing string
}
type Plan struct {
	Server    ServerRelease
	Mods      []PlannedMod
	Previous  State
	Inventory map[string]string
	Changed   bool
	// Adopted lists verified launchers that are replaced although not in state.
	Adopted map[string]fabricLauncherInfo
}

func fileHash(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func readState(root string) (State, error) {
	var s State
	path, err := safePath(root, ".mcupdater/state.json")
	if err != nil {
		return s, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 8<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(&s); err != nil {
		return s, fmt.Errorf("invalid updater state: %w", err)
	}
	s.Loader = normalizeLoader(s.Loader)
	for p := range s.Files {
		if _, err = safePath(root, p); err != nil {
			return s, err
		}
		if p == ".mcupdater/state.json" || strings.HasPrefix(p, ".mcupdater/") {
			return s, fmt.Errorf("invalid managed path %q", p)
		}
	}
	for _, m := range s.Mods {
		if m.File != "" && !safeJarName(m.File) {
			return s, fmt.Errorf("invalid installed mod filename")
		}
	}
	return s, nil
}

func inventory(root string, cfg Config, s State) (map[string]string, error) {
	path, err := safePath(root, "mods")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	tracked := map[string]bool{}
	for _, m := range cfg.Mods {
		if m.File != "" {
			tracked[m.File] = true
		}
	}
	// Previously managed mods removed from config are explicit removals.
	for _, m := range s.Mods {
		tracked[m.File] = true
	}
	result := map[string]string{}
	for _, e := range entries {
		if !strings.EqualFold(filepath.Ext(e.Name()), ".jar") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("mods/%s is not a regular JAR", e.Name())
		}
		if !tracked[e.Name()] {
			return nil, fmt.Errorf("untracked mod mods/%s: add its platform, project_id and file to config, or generate a fresh inventory with init", e.Name())
		}
		h, err := fileHash(filepath.Join(path, e.Name()))
		if err != nil {
			return nil, err
		}
		result[e.Name()] = h
	}
	return result, nil
}

func dependencySelected(mods []PlannedMod, platform string, d Dependency) bool {
	for _, other := range mods {
		if other.Missing != "" || other.Mod.Platform != platform {
			continue
		}
		if (d.ProjectID != "" && other.Release.ProjectID == d.ProjectID) || (d.ProjectID == "" && d.VersionID != "" && other.Release.VersionID == d.VersionID) {
			return d.VersionID == "" || other.Release.VersionID == d.VersionID
		}
	}
	return false
}

func (c *Client) BuildPlan(ctx context.Context, cfg Config) (Plan, error) {
	var p Plan
	var err error
	if err = ValidateConfig(cfg); err != nil {
		return p, err
	}
	p.Previous, err = readState(cfg.ServerDir)
	if err != nil {
		return p, err
	}
	p.Inventory, err = inventory(cfg.ServerDir, cfg, p.Previous)
	if err != nil {
		return p, err
	}
	p.Server, err = c.ResolveServer(ctx, cfg)
	if err != nil {
		return p, fmt.Errorf("server target: %w", err)
	}
	predicted := map[string]string{}
	for _, rel := range predictedServerFiles(p.Server) {
		predicted[rel] = ""
	}
	if err = checkOwnership(cfg, p, predicted); err != nil {
		return p, err
	}
	var failures []string
	canonical := map[string]bool{}
	for _, m := range cfg.Mods {
		r, e := c.ResolveMod(ctx, m, p.Server.Minecraft, p.Server.Loader.Kind, cfg.AllowPrerelease)
		pm := PlannedMod{Mod: m, Release: r}
		if e != nil {
			if !errors.Is(e, ErrUnavailable) {
				return p, fmt.Errorf("%s: %w", m.Label(), e)
			}
			pm.Missing = e.Error()
		} else {
			key := m.Platform + ":" + r.ProjectID
			if canonical[key] {
				return p, fmt.Errorf("duplicate configured project %s (including slug aliases)", key)
			}
			canonical[key] = true
			if !safeJarName(r.Artifact.Filename) {
				return p, fmt.Errorf("%s: unsafe release filename %q", m.Label(), r.Artifact.Filename)
			}
		}
		p.Mods = append(p.Mods, pm)
	}
	// Optional dependency loss propagates until stable; mandatory loss blocks.
	for changed := true; changed; {
		changed = false
		for i := range p.Mods {
			pm := &p.Mods[i]
			if pm.Missing != "" {
				continue
			}
			for _, d := range pm.Release.Dependencies {
				if d.Kind == "required" && !dependencySelected(p.Mods, pm.Mod.Platform, d) {
					pm.Missing = fmt.Sprintf("required dependency %s version %s is not selected; configure it explicitly", d.ProjectID, d.VersionID)
					changed = true
					break
				}
			}
		}
	}
	files := map[string]string{}
	for _, pm := range p.Mods {
		if pm.Missing != "" {
			if pm.Mod.Required() {
				failures = append(failures, pm.Mod.Label()+": "+pm.Missing)
			}
			continue
		}
		// Only surviving mods participate in conflicts, after dependency loss settles.
		for _, d := range pm.Release.Dependencies {
			if d.Kind == "incompatible" && dependencySelected(p.Mods, pm.Mod.Platform, d) {
				return p, fmt.Errorf("%s conflicts with selected project %s version %s", pm.Mod.Label(), d.ProjectID, d.VersionID)
			}
		}
		name := strings.ToLower(pm.Release.Artifact.Filename)
		if other, ok := files[name]; ok {
			return p, fmt.Errorf("filename collision between %s and %s", other, pm.Mod.Label())
		}
		files[name] = pm.Mod.Label()
	}
	if len(failures) > 0 {
		return p, fmt.Errorf("mandatory mods unavailable; no files changed:\n  %s", strings.Join(failures, "\n  "))
	}
	p.Changed = p.Server.Minecraft != p.Previous.Minecraft || p.Server.Loader != p.Previous.Loader
	if p.Server.Loader.Layout == "launcher" && p.Server.InstallerVersion != p.Previous.Installer {
		p.Changed = true
	}
	if len(p.Previous.Mods) != len(p.Mods) || len(p.Inventory) != len(files) {
		p.Changed = true
	}
	for _, pm := range p.Mods {
		old, ok := p.Previous.Mods[pm.Mod.Key()]
		if pm.Missing != "" {
			if !ok || old.File != "" {
				p.Changed = true
			}
			continue
		}
		if !ok || old.Version != pm.Release.VersionID || old.File != pm.Release.Artifact.Filename || old.SHA256 == "" || p.Inventory[old.File] != old.SHA256 {
			p.Changed = true
		}
	}
	for name, digest := range p.Previous.Files {
		if name == "user_jvm_args.txt" {
			continue
		}
		path, e := safePath(cfg.ServerDir, name)
		if e != nil {
			return p, e
		}
		got, e := fileHash(path)
		if errors.Is(e, os.ErrNotExist) {
			p.Changed = true
			continue
		}
		if e != nil {
			return p, e
		}
		if got != digest {
			p.Changed = true
		}
	}
	return p, nil
}

func (p Plan) Print(w io.Writer) {
	if p.Server.Loader.Kind == "vanilla" {
		fmt.Fprintf(w, "Target: Minecraft %s, vanilla (Java %d+)\n", p.Server.Minecraft, p.Server.JavaMajor)
	} else {
		fmt.Fprintf(w, "Target: Minecraft %s, %s %s (Java %d+)\n", p.Server.Minecraft, p.Server.Loader.Kind, p.Server.Loader.Version, p.Server.JavaMajor)
	}
	if p.Previous.Minecraft != "" {
		if p.Previous.Loader.Kind == "vanilla" {
			fmt.Fprintf(w, "Installed: Minecraft %s, vanilla\n", p.Previous.Minecraft)
		} else {
			fmt.Fprintf(w, "Installed: Minecraft %s, %s %s\n", p.Previous.Minecraft, p.Previous.Loader.Kind, p.Previous.Loader.Version)
		}
	}
	for _, pm := range p.Mods {
		if pm.Missing != "" {
			fmt.Fprintf(w, "WARNING: optional mod %s unavailable: %s\n  It will be omitted; any currently installed JAR is removed and backed up.\n", pm.Mod.Label(), pm.Missing)
		} else {
			fmt.Fprintf(w, "  %s -> %s [%s]\n", pm.Mod.Label(), pm.Release.Version, pm.Release.Artifact.Filename)
		}
	}
	selected := map[string]bool{}
	for _, m := range p.Mods {
		selected[m.Mod.Key()] = true
	}
	var removed []string
	for k := range p.Previous.Mods {
		if !selected[k] {
			removed = append(removed, k)
		}
	}
	sort.Strings(removed)
	for _, k := range removed {
		fmt.Fprintf(w, "Remove no-longer-configured mod: %s\n", k)
	}
	if !p.Changed {
		fmt.Fprintln(w, "Already up to date.")
	}
	if p.Server.Loader.Kind != "vanilla" {
		fmt.Fprintln(w, "Compatibility uses published Minecraft/loader tags and dependency metadata; it does not prove runtime compatibility or exact loader-version constraints.")
	}
}
