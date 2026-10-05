package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type InstalledMod struct {
	File          string    `json:"file"`
	Version       string    `json:"version"`
	SHA256        string    `json:"sha256"`
	VersionNumber string    `json:"version_number,omitempty"`
	Published     time.Time `json:"published,omitzero"`
	Channel       string    `json:"channel,omitempty"`
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
	Mod       Mod
	Release   ModRelease
	Missing   string
	Installed *InstalledVersion // nil when the mod is not installed
}

// InstalledVersion is what is currently installed; an empty Number means the
// JAR is present but its version could not be identified.
type InstalledVersion struct {
	Number, Channel string
	Published       time.Time
}
type Plan struct {
	Server    ServerRelease
	Mods      []PlannedMod
	Previous  State
	Inventory map[string]string
	Changed   bool
	// Adopted lists verified launchers that are replaced although not in state.
	Adopted map[string]adoptedLauncher
	// Conversion is set when the Fabric layout or launcher file changes.
	Conversion *LayoutConversion
}

// LayoutConversion describes a change of Fabric layout so it is never silent.
type LayoutConversion struct {
	From, To string
	Remove   []string // managed files moved to the backup
	Write    []string
}

func describeLayout(l LoaderConfig, installer string) string {
	if l.Layout != "launcher" {
		return l.Layout
	}
	if installer != "" {
		return "launcher (" + l.LauncherFile + ", installer " + installer + ")"
	}
	return "launcher (" + l.LauncherFile + ")"
}

func planConversion(p Plan) *LayoutConversion {
	prev, next := p.Previous.Loader, p.Server.Loader
	if prev.Kind != "fabric" || next.Kind != "fabric" || (prev.Layout == next.Layout && prev.LauncherFile == next.LauncherFile) {
		return nil
	}
	conv := &LayoutConversion{From: describeLayout(prev, ""), To: describeLayout(next, ""), Write: predictedServerFiles(p.Server)}
	keep := map[string]bool{}
	for _, w := range conv.Write {
		keep[w] = true
	}
	for name := range p.Previous.Files {
		if name != "user_jvm_args.txt" && !keep[name] {
			conv.Remove = append(conv.Remove, name)
		}
	}
	sort.Strings(conv.Remove)
	return conv
}

// summarizePaths collapses libraries/** into one "libraries/ (N files)" item.
func summarizePaths(paths []string) string {
	var out []string
	libs := 0
	for _, p := range paths {
		if strings.HasPrefix(p, "libraries/") {
			libs++
		} else {
			out = append(out, p)
		}
	}
	if libs > 0 {
		out = append(out, fmt.Sprintf("libraries/ (%d files)", libs))
	}
	return strings.Join(out, ", ")
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
	p.Conversion = planConversion(p)
	if err = c.adoptLauncher(ctx, cfg, &p); err != nil {
		return p, err
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
		pm.Installed, err = c.installedVersion(ctx, cfg.ServerDir, p.Previous, m, r.ProjectID)
		if err != nil {
			return p, err
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
	if (p.Server.Loader.Layout == "launcher" && p.Server.InstallerVersion != p.Previous.Installer) || len(p.Adopted) > 0 {
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
	} else if p.Server.Loader.Kind == "fabric" {
		fmt.Fprintf(w, "Target: Minecraft %s, fabric %s, %s (Java %d+)\n", p.Server.Minecraft, p.Server.Loader.Version, describeLayout(p.Server.Loader, p.Server.InstallerVersion), p.Server.JavaMajor)
	} else {
		fmt.Fprintf(w, "Target: Minecraft %s, %s %s (Java %d+)\n", p.Server.Minecraft, p.Server.Loader.Kind, p.Server.Loader.Version, p.Server.JavaMajor)
	}
	if p.Previous.Minecraft != "" {
		switch p.Previous.Loader.Kind {
		case "vanilla":
			fmt.Fprintf(w, "Installed: Minecraft %s, vanilla\n", p.Previous.Minecraft)
		case "fabric":
			fmt.Fprintf(w, "Installed: Minecraft %s, fabric %s, %s\n", p.Previous.Minecraft, p.Previous.Loader.Version, describeLayout(p.Previous.Loader, p.Previous.Installer))
		default:
			fmt.Fprintf(w, "Installed: Minecraft %s, %s %s\n", p.Previous.Minecraft, p.Previous.Loader.Kind, p.Previous.Loader.Version)
		}
	}
	if c := p.Conversion; c != nil {
		fmt.Fprintf(w, "Convert Fabric layout: %s -> %s\n", c.From, c.To)
		if len(c.Remove) > 0 {
			fmt.Fprintf(w, "  remove (moved to backup): %s\n", summarizePaths(c.Remove))
		}
		fmt.Fprintf(w, "  write: %s\n", strings.Join(c.Write, ", "))
	}
	for _, file := range sortedKeys(p.Adopted) {
		info := p.Adopted[file]
		fmt.Fprintf(w, "Adopt Fabric launcher %s: verified as fabric-installer %s (loader %s, Minecraft %s); it will be replaced and backed up.\n",
			file, info.Installer, info.Loader, info.Minecraft)
	}
	downgrades := 0
	for _, pm := range p.Mods {
		if pm.Missing != "" {
			installed := ""
			if pm.Installed != nil && pm.Installed.Number != "" {
				installed = " (installed: " + pm.Installed.Number + ")"
			}
			fmt.Fprintf(w, "WARNING: optional mod %s%s unavailable: %s\n  It will be omitted; any currently installed JAR is removed and backed up.\n", pm.Mod.Label(), installed, pm.Missing)
			continue
		}
		kind := classify(pm)
		tag := fmt.Sprintf("%-11s", "["+kind+"]")
		switch kind {
		case "same":
			fmt.Fprintf(w, "  %s %s %s\n", tag, pm.Mod.Label(), pm.Release.Version)
		case "new":
			fmt.Fprintf(w, "  %s %s -> %s [%s]\n", tag, pm.Mod.Label(), pm.Release.Version, pm.Release.Artifact.Filename)
		case "unknown":
			fmt.Fprintf(w, "  %s %s ? -> %s [%s]\n", tag, pm.Mod.Label(), pm.Release.Version, pm.Release.Artifact.Filename)
		default:
			fmt.Fprintf(w, "  %s %s %s -> %s [%s]\n", tag, pm.Mod.Label(), pm.Installed.Number, pm.Release.Version, pm.Release.Artifact.Filename)
		}
		if kind == "DOWNGRADE" {
			downgrades++
			if pm.Installed.Channel == "alpha" || pm.Installed.Channel == "beta" {
				fmt.Fprintf(w, "      installed version is an %s release and allow_prerelease is false; set \"allow_prerelease\": true to keep it, or accept the downgrade\n", pm.Installed.Channel)
			} else {
				fmt.Fprintln(w, "      the selected release was published before the installed one")
			}
		}
	}
	if downgrades > 0 {
		fmt.Fprintf(w, "WARNING: %d mod(s) would be downgraded.\n", downgrades)
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

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// classify compares the installed and selected versions of a resolved mod.
func classify(pm PlannedMod) string {
	switch {
	case pm.Installed == nil:
		return "new"
	case pm.Installed.Number == "":
		return "unknown"
	case pm.Installed.Number == pm.Release.Version:
		return "same"
	case !pm.Installed.Published.IsZero() && !pm.Release.Published.IsZero() && pm.Release.Published.Before(pm.Installed.Published):
		return "DOWNGRADE"
	default:
		return "update"
	}
}

// installedVersion reports the installed version of a configured mod: from state
// when recorded, otherwise by looking up the JAR's hash on Modrinth.
func (c *Client) installedVersion(ctx context.Context, root string, previous State, m Mod, resolvedProject string) (*InstalledVersion, error) {
	file := m.File
	if rec, ok := previous.Mods[m.Key()]; ok {
		if rec.File == "" {
			return nil, nil
		}
		if rec.VersionNumber != "" {
			return &InstalledVersion{Number: rec.VersionNumber, Channel: rec.Channel, Published: rec.Published}, nil
		}
		file = rec.File // state written before version details were recorded
	}
	if file == "" {
		return nil, nil
	}
	live, err := safePath(root, "mods/"+file)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(live)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if m.Platform != "modrinth" {
		return &InstalledVersion{}, nil
	}
	sum, _, err := modIdentityHash(ctx, f)
	if err != nil {
		return nil, err
	}
	var v modrinthVersion
	if err := c.GetJSON(ctx, modrinthAPI+"/version_file/"+sum+"?algorithm=sha1", &v); err != nil {
		var status *HTTPError
		if errors.As(err, &status) && status.StatusCode == http.StatusNotFound {
			return &InstalledVersion{}, nil
		}
		return nil, fmt.Errorf("look up installed version of mods/%s: %w", file, err)
	}
	if v.Version == "" || v.ProjectID == "" {
		return nil, fmt.Errorf("malformed Modrinth version for mods/%s", file)
	}
	if resolvedProject != "" && v.ProjectID != resolvedProject {
		return nil, fmt.Errorf("mods/%s belongs to Modrinth project %s, not %s", file, v.ProjectID, resolvedProject)
	}
	return &InstalledVersion{Number: v.Version, Channel: v.ReleaseType, Published: v.Published}, nil
}
