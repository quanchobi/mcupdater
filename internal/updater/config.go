package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]*$`)

func LoadConfig(filename string) (Config, error) {
	var cfg Config
	f, err := os.Open(filename)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 4<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return cfg, fmt.Errorf("config must contain exactly one JSON object")
	}
	if cfg.ServerDir == "" {
		cfg.ServerDir = "."
	}
	if !filepath.IsAbs(cfg.ServerDir) {
		cfg.ServerDir = filepath.Join(filepath.Dir(filename), cfg.ServerDir)
	}
	cfg.ServerDir, err = filepath.Abs(cfg.ServerDir)
	if err != nil {
		return cfg, err
	}
	if cfg.Minecraft == "" {
		cfg.Minecraft = "latest"
	}
	if cfg.Loader.Version == "" && cfg.Loader.Kind != "vanilla" {
		cfg.Loader.Version = "latest"
	}
	if cfg.Java == "" {
		cfg.Java = "java"
	}
	if strings.ContainsAny(cfg.Java, "/\\") && !filepath.IsAbs(cfg.Java) {
		cfg.Java, err = filepath.Abs(filepath.Join(filepath.Dir(filename), cfg.Java))
		if err != nil {
			return cfg, err
		}
	}
	if err = ValidateConfig(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func ValidateConfig(cfg Config) error {
	switch cfg.Loader.Kind {
	case "vanilla":
		if cfg.Loader.Version != "" && cfg.Loader.Version != "latest" {
			return fmt.Errorf("vanilla uses minecraft for its version; omit loader.version or use latest")
		}
		if len(cfg.Mods) != 0 {
			return fmt.Errorf("vanilla does not support mods; mods must be empty")
		}
	case "fabric", "quilt", "forge", "neoforge":
	default:
		return fmt.Errorf("loader.kind must be vanilla, fabric, quilt, forge, or neoforge")
	}
	if !tokenPattern.MatchString(cfg.Minecraft) || (cfg.Loader.Kind != "vanilla" && !tokenPattern.MatchString(cfg.Loader.Version)) {
		return fmt.Errorf("invalid Minecraft or loader version")
	}
	seen := map[string]bool{}
	files := map[string]bool{}
	for _, m := range cfg.Mods {
		if m.Platform != "modrinth" && m.Platform != "curseforge" {
			return fmt.Errorf("%s: platform must be modrinth or curseforge", m.Label())
		}
		if !tokenPattern.MatchString(m.ProjectID) {
			return fmt.Errorf("invalid project ID %q", m.ProjectID)
		}
		if seen[m.Key()] {
			return fmt.Errorf("duplicate mod %s", m.Key())
		}
		seen[m.Key()] = true
		if m.File != "" {
			if !safeJarName(m.File) {
				return fmt.Errorf("%s: file must be a JAR basename", m.Label())
			}
			key := strings.ToLower(m.File)
			if files[key] {
				return fmt.Errorf("duplicate mod file %q", m.File)
			}
			files[key] = true
		}
	}
	return nil
}

func safeJarName(s string) bool {
	if s == "" || filepath.Base(s) != s || strings.ContainsAny(s, "/\\:<>\"|?*") || strings.HasPrefix(s, ".") || !strings.EqualFold(filepath.Ext(s), ".jar") {
		return false
	}
	for _, r := range s {
		if r < 32 || r == 127 {
			return false
		}
	}
	base := strings.ToUpper(strings.SplitN(s, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return false
	}
	return true
}

// Discover never guesses project identities from filenames. An unknown file aborts
// the entire inventory so it cannot silently disappear from future updates.
func (c *Client) Discover(ctx context.Context, serverDir string) ([]Mod, error) {
	entries, err := os.ReadDir(filepath.Join(serverDir, "mods"))
	if errors.Is(err, os.ErrNotExist) {
		return []Mod{}, nil
	}
	if err != nil {
		return nil, err
	}
	mods := make([]Mod, 0)
	seen := map[string]bool{}
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
		m, err := c.IdentifyMod(ctx, filepath.Join(serverDir, "mods", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("identify %s: %w", e.Name(), err)
		}
		if seen[m.Key()] {
			return nil, fmt.Errorf("multiple installed JARs belong to %s; remove duplicates first", m.Key())
		}
		seen[m.Key()] = true
		m.File = e.Name()
		mods = append(mods, m)
	}
	return mods, nil
}

func WriteConfig(filename string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		os.Remove(filename)
		return err
	}
	return closeErr
}
