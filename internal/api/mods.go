package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"time"

	"github.com/quanchobi/mcupdater/internal/config"
	"github.com/quanchobi/mcupdater/internal/logger"
)

type ModFile struct {
	ID       string `json:"id"`
	Hashes   struct {
		Sha1   string `json:"sha1"`
		Sha512 string `json:"sha512"`
	} `json:"hashes"`
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Primary  bool   `json:"primary"`
	Size     int    `json:"size"`
	FileType any    `json:"file_type"`
}

type ModInfo struct {
	GameVersions    []string  `json:"game_versions"`
	Loaders         []string  `json:"loaders"`
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id"`
	AuthorID        string    `json:"author_id"`
	Featured        bool      `json:"featured"`
	Name            string    `json:"name"`
	VersionNumber   string    `json:"version_number"`
	Changelog       string    `json:"changelog"`
	ChangelogURL    any       `json:"changelog_url"`
	DatePublished   time.Time `json:"date_published"`
	Downloads       int       `json:"downloads"`
	VersionType     string    `json:"version_type"`
	Status          string    `json:"status"`
	RequestedStatus any       `json:"requested_status"`
	Files           []ModFile `json:"files"`
	Dependencies    []any     `json:"dependencies"`
}

type ModChange struct {
	Name       string
	OldVersion string
	NewVersion string
	IsNew      bool
	IsUpdate   bool
	Required   bool
}

func getModrinthModInfo(client *http.Client, slug string, loader string, mcVersion string) ([]ModInfo, error) {
	baseURL := fmt.Sprintf("https://api.modrinth.com/v2/project/%s/version", slug)
	params := url.Values{}

	params.Add("loaders", fmt.Sprintf("[\"%s\"]", loader))
	params.Add("game_version", fmt.Sprintf("[\"%s\"]", mcVersion))

	reqURL, err := url.Parse(baseURL)
	if err != nil {
		return []ModInfo{}, err
	}

	reqURL.RawQuery = params.Encode()

	req, err := http.NewRequest("GET", reqURL.String(), nil)
	if err != nil {
		return []ModInfo{}, err
	}

	req.Header.Set("User-Agent", "github_quanchobi/mcupdater/0.1 (contact@quanchobi.io)")

	resp, err := client.Do(req)
	if err != nil {
		return []ModInfo{}, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return []ModInfo{}, err
	}

	mods := []ModInfo{}

	err = json.Unmarshal(data, &mods)
	if err != nil {
		return []ModInfo{}, err
	}
	return mods, nil
}

func selectVersionAndFile(versions []ModInfo, requestedVersion string) (ModInfo, ModFile, error) {
	if len(versions) == 0 {
		return ModInfo{}, ModFile{}, fmt.Errorf("no versions available")
	}

	sorted := make([]ModInfo, len(versions))
	copy(sorted, versions)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].DatePublished.After(sorted[j].DatePublished)
	})

	targetVersion := sorted[0]
	if requestedVersion != "" {
		found := false
		for _, v := range sorted {
			if v.VersionNumber == requestedVersion {
				targetVersion = v
				found = true
				break
			}
		}
		if !found {
			return ModInfo{}, ModFile{}, fmt.Errorf("version %q not found", requestedVersion)
		}
	}

	if len(targetVersion.Files) == 0 {
		return ModInfo{}, ModFile{}, fmt.Errorf("no files available for version %s", targetVersion.VersionNumber)
	}

	file := targetVersion.Files[0]
	for _, f := range targetVersion.Files {
		if f.Primary {
			file = f
			break
		}
	}

	return targetVersion, file, nil
}

func ResolveModChanges(cfg config.Config, log *logger.Logger) ([]ModChange, error) {
	return resolveModChangesWithClient(http.DefaultClient, cfg, log)
}

func resolveModChangesWithClient(client *http.Client, cfg config.Config, log *logger.Logger) ([]ModChange, error) {
	if err := os.MkdirAll(cfg.Config.ModsPath, 0755); err != nil {
		return nil, err
	}

	existingMods := make(map[string]string)
	entries, err := os.ReadDir(cfg.Config.ModsPath)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if len(name) > 4 && name[len(name)-4:] == ".jar" {
				name = name[:len(name)-4]
			}
			existingMods[name] = entry.Name()
		}
	}

	var changes []ModChange

	for _, mod := range cfg.Mods.Required {
		change, err := resolveSingleModWithClient(client, mod, true, cfg, existingMods, log)
		if err != nil {
			return changes, fmt.Errorf("required mod %q: %w", mod.Name, err)
		}
		if change != nil {
			changes = append(changes, *change)
		}
	}

	for _, mod := range cfg.Mods.Optional {
		change, err := resolveSingleModWithClient(client, mod, false, cfg, existingMods, log)
		if err != nil {
			log.Warn("optional mod %q unavailable: %v", mod.Name, err)
			continue
		}
		if change != nil {
			changes = append(changes, *change)
		}
	}

	return changes, nil
}

func resolveSingleModWithClient(client *http.Client, mod config.ModEntry, required bool, cfg config.Config, existingMods map[string]string, log *logger.Logger) (*ModChange, error) {
	loader := cfg.Loader.Name
	mcVersion := cfg.Minecraft.Version

	versions, err := getModrinthModInfo(client, mod.Name, loader, mcVersion)
	if err != nil {
		return nil, err
	}

	targetVersion, file, err := selectVersionAndFile(versions, mod.Version)
	if err != nil {
		return nil, err
	}

	dstPath := cfg.Config.ModsPath + "/" + file.Filename

	var change ModChange
	change.Name = mod.Name
	change.NewVersion = targetVersion.VersionNumber
	change.Required = required

	existingFile, exists := existingMods[file.Filename]
	if exists {
		change.OldVersion = existingFile
		change.IsUpdate = true
	} else {
		change.IsNew = true
	}

	log.Info("mod %s: %s -> %s", mod.Name, targetVersion.VersionNumber, file.Filename)

	err = downloadFile(client, dstPath, file.URL)
	if err != nil {
		return nil, err
	}

	return &change, nil
}
