package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	modrinthAPI   = "https://api.modrinth.com/v2"
	curseForgeAPI = "https://api.curseforge.com/v1"
)

// ResolveMod selects a release explicitly tagged for both the game and loader.
// Platform failures remain fatal; only absence of a usable release is unavailable.
func (c *Client) ResolveMod(ctx context.Context, mod Mod, minecraft, loader string, allowPrerelease bool) (ModRelease, error) {
	if minecraft == "" || mod.ProjectID == "" {
		return ModRelease{}, fmt.Errorf("mod resolution requires a project and Minecraft version")
	}
	switch loader {
	case "fabric", "quilt", "forge", "neoforge":
	default:
		return ModRelease{}, fmt.Errorf("unsupported mod loader %q", loader)
	}
	switch mod.Platform {
	case "modrinth":
		return c.resolveModrinth(ctx, mod.ProjectID, minecraft, loader, allowPrerelease)
	case "curseforge":
		return c.resolveCurseForge(ctx, mod.ProjectID, minecraft, loader, allowPrerelease)
	default:
		return ModRelease{}, fmt.Errorf("unsupported mod platform %q", mod.Platform)
	}
}

func modNotFound(err error, description string) error {
	var status *HTTPError
	if errors.As(err, &status) && status.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s: %w", description, ErrUnavailable)
	}
	return fmt.Errorf("%s: %w", description, err)
}

func modHasTag(tags []string, tag string) bool {
	for _, value := range tags {
		if value == tag {
			return true
		}
	}
	return false
}

func modHash(value string, bytes int) bool {
	if len(value) != bytes*2 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') && !(character >= 'A' && character <= 'F') {
			return false
		}
	}
	return true
}

func modArtifactURL(address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("malformed artifact URL %q", address)
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return fmt.Errorf("refusing non-HTTPS artifact URL %q", address)
	}
	return nil
}

type modrinthProject struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	ProjectType string   `json:"project_type"`
	ServerSide  string   `json:"server_side"`
	Environment []string `json:"environment"`
}

type modrinthFile struct {
	Hashes   map[string]string `json:"hashes"`
	URL      string            `json:"url"`
	Filename string            `json:"filename"`
	Primary  bool              `json:"primary"`
	Size     int64             `json:"size"`
}

type modrinthVersion struct {
	ID           string         `json:"id"`
	ProjectID    string         `json:"project_id"`
	Version      string         `json:"version_number"`
	Published    time.Time      `json:"date_published"`
	ReleaseType  string         `json:"version_type"`
	GameVersions []string       `json:"game_versions"`
	Loaders      []string       `json:"loaders"`
	Environment  string         `json:"environment"`
	Files        []modrinthFile `json:"files"`
	Dependencies []struct {
		ProjectID string `json:"project_id"`
		VersionID string `json:"version_id"`
		Kind      string `json:"dependency_type"`
	} `json:"dependencies"`
}

func (c *Client) modrinthProject(ctx context.Context, id string) (modrinthProject, error) {
	var project modrinthProject
	if err := c.GetJSON(ctx, modrinthAPI+"/project/"+url.PathEscape(id), &project); err != nil {
		return project, modNotFound(err, "get Modrinth project "+id)
	}
	if project.ID == "" || project.Title == "" || project.ProjectType == "" {
		return project, fmt.Errorf("malformed Modrinth project %q", id)
	}
	return project, nil
}

func modrinthClientOnly(environment string) bool {
	return environment == "client_only" || environment == "singleplayer_only"
}

func modrinthServerProject(project modrinthProject) bool {
	if project.ProjectType != "mod" {
		return false
	}
	// The legacy side flag samples one version; environments cover the project.
	if len(project.Environment) != 0 {
		for _, environment := range project.Environment {
			if !modrinthClientOnly(environment) {
				return true
			}
		}
		return false
	}
	return project.ServerSide != "unsupported"
}

func (version modrinthVersion) validate() error {
	if version.ID == "" || version.ProjectID == "" || version.Version == "" || version.Published.IsZero() || version.GameVersions == nil || version.Loaders == nil || version.Files == nil {
		return fmt.Errorf("malformed Modrinth version %q", version.ID)
	}
	switch version.ReleaseType {
	case "release", "beta", "alpha":
		return nil
	default:
		return fmt.Errorf("malformed Modrinth release type %q", version.ReleaseType)
	}
}

func modrinthArtifact(files []modrinthFile) (Artifact, error) {
	chosen, jars, primaries := -1, 0, 0
	for i, file := range files {
		if file.Filename == "" {
			return Artifact{}, fmt.Errorf("malformed Modrinth artifact filename")
		}
		if file.Primary {
			primaries++
		}
		if !strings.EqualFold(".jar", filepath.Ext(file.Filename)) {
			continue
		}
		if !safeJarName(file.Filename) {
			return Artifact{}, fmt.Errorf("unsafe artifact filename %q", file.Filename)
		}
		jars++
		if chosen == -1 || file.Primary {
			chosen = i
		}
	}
	if primaries > 1 {
		return Artifact{}, fmt.Errorf("Modrinth version has multiple primary files")
	}
	if chosen == -1 {
		return Artifact{}, fmt.Errorf("Modrinth version has no JAR: %w", ErrUnavailable)
	}
	file := files[chosen]
	if jars > 1 && !file.Primary {
		return Artifact{}, fmt.Errorf("Modrinth version has ambiguous JAR files without a primary")
	}
	if err := modArtifactURL(file.URL); err != nil {
		return Artifact{}, err
	}
	if file.Size <= 0 {
		return Artifact{}, fmt.Errorf("malformed Modrinth artifact size for %q", file.Filename)
	}
	if value, present := file.Hashes["sha512"]; present && !modHash(value, 64) {
		return Artifact{}, fmt.Errorf("malformed Modrinth SHA512 hash for %q", file.Filename)
	}
	if value, present := file.Hashes["sha1"]; present && !modHash(value, 20) {
		return Artifact{}, fmt.Errorf("malformed Modrinth SHA1 hash for %q", file.Filename)
	}
	algorithm, hash := "sha512", file.Hashes["sha512"]
	if hash == "" {
		algorithm, hash = "sha1", file.Hashes["sha1"]
	}
	if hash == "" {
		return Artifact{}, fmt.Errorf("Modrinth artifact %q has no supported hash", file.Filename)
	}
	return Artifact{URL: file.URL, Filename: file.Filename, HashAlgorithm: algorithm, Hash: hash, Size: file.Size}, nil
}

func (c *Client) resolveModrinth(ctx context.Context, id, minecraft, loader string, allowPrerelease bool) (ModRelease, error) {
	project, err := c.modrinthProject(ctx, id)
	if err != nil {
		return ModRelease{}, err
	}
	if !modrinthServerProject(project) {
		return ModRelease{}, fmt.Errorf("Modrinth project %s is not a server mod: %w", project.ID, ErrUnavailable)
	}
	games, _ := json.Marshal([]string{minecraft})
	loaders, _ := json.Marshal([]string{loader})
	query := url.Values{"game_versions": {string(games)}, "loaders": {string(loaders)}, "include_changelog": {"false"}}
	var versions []modrinthVersion
	if err := c.GetJSON(ctx, modrinthAPI+"/project/"+url.PathEscape(project.ID)+"/version?"+query.Encode(), &versions); err != nil {
		return ModRelease{}, modNotFound(err, "list Modrinth versions")
	}
	if versions == nil {
		return ModRelease{}, fmt.Errorf("malformed Modrinth version list")
	}
	var best *modrinthVersion
	var artifact Artifact
	for i := range versions {
		version := &versions[i]
		if err := version.validate(); err != nil {
			return ModRelease{}, err
		}
		if version.ProjectID != project.ID {
			return ModRelease{}, fmt.Errorf("Modrinth returned a version of an unrelated project")
		}
		if !modHasTag(version.GameVersions, minecraft) || !modHasTag(version.Loaders, loader) || (!allowPrerelease && version.ReleaseType != "release") || modrinthClientOnly(version.Environment) {
			continue
		}
		candidate, err := modrinthArtifact(version.Files)
		if errors.Is(err, ErrUnavailable) {
			continue
		}
		if err != nil {
			return ModRelease{}, fmt.Errorf("Modrinth version %s: %w", version.ID, err)
		}
		if best == nil || version.Published.After(best.Published) || (version.Published.Equal(best.Published) && version.ID > best.ID) {
			best, artifact = version, candidate
		}
	}
	if best == nil {
		return ModRelease{}, fmt.Errorf("Modrinth project %s for %s/%s: %w", project.ID, minecraft, loader, ErrUnavailable)
	}
	dependencies, err := c.modrinthDependencies(ctx, *best)
	if err != nil {
		return ModRelease{}, err
	}
	return ModRelease{ProjectID: project.ID, VersionID: best.ID, Version: best.Version, Artifact: artifact, Dependencies: dependencies,
		Published: best.Published, Channel: best.ReleaseType}, nil
}

func (c *Client) modrinthDependencies(ctx context.Context, version modrinthVersion) ([]Dependency, error) {
	var dependencies []Dependency
	for _, dependency := range version.Dependencies {
		switch dependency.Kind {
		case "optional", "embedded":
			continue
		case "required", "incompatible":
		default:
			return nil, fmt.Errorf("malformed Modrinth dependency type %q", dependency.Kind)
		}
		project := dependency.ProjectID
		if dependency.VersionID != "" && project == "" {
			var required modrinthVersion
			if err := c.GetJSON(ctx, modrinthAPI+"/version/"+url.PathEscape(dependency.VersionID), &required); err != nil {
				return nil, modNotFound(err, "resolve Modrinth dependency "+dependency.VersionID)
			}
			if required.ID != dependency.VersionID || required.ProjectID == "" {
				return nil, fmt.Errorf("malformed Modrinth dependency %q", dependency.VersionID)
			}
			project = required.ProjectID
		}
		if project == "" {
			return nil, fmt.Errorf("Modrinth %s dependency has no project or version identity", dependency.Kind)
		}
		dependencies = append(dependencies, Dependency{ProjectID: project, VersionID: dependency.VersionID, Kind: dependency.Kind})
	}
	return dependencies, nil
}

type curseForgeProject struct {
	ID                uint32 `json:"id"`
	GameID            uint32 `json:"gameId"`
	ClassID           uint32 `json:"classId"`
	Name              string `json:"name"`
	AllowDistribution *bool  `json:"allowModDistribution"`
}

// A null downloadUrl means the author disabled downloads. A missing field is
// malformed metadata and must not silently turn an outage into unavailability.
type curseForgeDownloadURL struct {
	Value   string
	Present bool
}

func (address *curseForgeDownloadURL) UnmarshalJSON(data []byte) error {
	address.Present = true
	return json.Unmarshal(data, &address.Value)
}

type curseForgeFile struct {
	ID           uint32                `json:"id"`
	ModID        uint32                `json:"modId"`
	GameID       uint32                `json:"gameId"`
	Available    *bool                 `json:"isAvailable"`
	Name         string                `json:"displayName"`
	Filename     string                `json:"fileName"`
	ReleaseType  int                   `json:"releaseType"`
	Date         time.Time             `json:"fileDate"`
	Size         int64                 `json:"fileLength"`
	DownloadURL  curseForgeDownloadURL `json:"downloadUrl"`
	GameVersions []string              `json:"gameVersions"`
	Fingerprint  *uint32               `json:"fileFingerprint"`
	Hashes       []struct {
		Value     string `json:"value"`
		Algorithm int    `json:"algo"`
	} `json:"hashes"`
	Dependencies []struct {
		ModID        uint32 `json:"modId"`
		RelationType int    `json:"relationType"`
	} `json:"dependencies"`
}

func curseForgeID(id string) (uint32, error) {
	for _, character := range id {
		if character < '0' || character > '9' {
			return 0, fmt.Errorf("CurseForge project ID must be a positive number, got %q", id)
		}
	}
	number, err := strconv.ParseUint(id, 10, 32)
	if err != nil || number == 0 {
		return 0, fmt.Errorf("CurseForge project ID must be a positive number, got %q", id)
	}
	return uint32(number), nil
}

func (c *Client) curseForgeJSON(ctx context.Context, method, endpoint string, body, out any) error {
	if strings.TrimSpace(c.CurseForgeKey) == "" {
		return fmt.Errorf("CurseForge access requires an API key; set CURSEFORGE_API_KEY")
	}
	return c.JSON(ctx, method, curseForgeAPI+endpoint, body, map[string]string{"x-api-key": c.CurseForgeKey}, out)
}

func (c *Client) curseForgeProject(ctx context.Context, id uint32) (curseForgeProject, error) {
	var response struct {
		Data *curseForgeProject `json:"data"`
	}
	if err := c.curseForgeJSON(ctx, http.MethodGet, "/mods/"+strconv.FormatUint(uint64(id), 10), nil, &response); err != nil {
		return curseForgeProject{}, modNotFound(err, "get CurseForge project")
	}
	if response.Data == nil || response.Data.ID != id || response.Data.Name == "" || response.Data.GameID == 0 {
		return curseForgeProject{}, fmt.Errorf("malformed CurseForge project %d", id)
	}
	if response.Data.GameID != 432 || (response.Data.ClassID != 0 && response.Data.ClassID != 6) {
		return curseForgeProject{}, fmt.Errorf("CurseForge project %d is not a Minecraft mod: %w", id, ErrUnavailable)
	}
	return *response.Data, nil
}

func curseForgeLoader(loader string) (int, string) {
	switch loader {
	case "forge":
		return 1, "Forge"
	case "fabric":
		return 4, "Fabric"
	case "quilt":
		return 5, "Quilt"
	case "neoforge":
		return 6, "NeoForge"
	default:
		return 0, ""
	}
}

func (file curseForgeFile) validate(project uint32) error {
	if file.ID == 0 || file.ModID != project || file.GameID != 432 || file.Available == nil || file.Name == "" || file.Filename == "" || file.Date.IsZero() || file.GameVersions == nil || file.ReleaseType < 1 || file.ReleaseType > 3 {
		return fmt.Errorf("malformed CurseForge file %d", file.ID)
	}
	if strings.EqualFold(filepath.Ext(file.Filename), ".jar") && !safeJarName(file.Filename) {
		return fmt.Errorf("unsafe artifact filename %q", file.Filename)
	}
	return nil
}

func curseForgeArtifact(file curseForgeFile) (Artifact, error) {
	if file.Available == nil {
		return Artifact{}, fmt.Errorf("malformed CurseForge file availability")
	}
	if !file.DownloadURL.Present {
		return Artifact{}, fmt.Errorf("malformed CurseForge file: missing downloadUrl")
	}
	if !*file.Available || file.DownloadURL.Value == "" || !strings.EqualFold(filepath.Ext(file.Filename), ".jar") {
		return Artifact{}, ErrUnavailable
	}
	if err := modArtifactURL(file.DownloadURL.Value); err != nil {
		return Artifact{}, err
	}
	if file.Size <= 0 {
		return Artifact{}, fmt.Errorf("malformed CurseForge file size for %d", file.ID)
	}
	sha1Hash, md5Hash := "", ""
	for _, hash := range file.Hashes {
		switch hash.Algorithm {
		case 1:
			if !modHash(hash.Value, 20) || (sha1Hash != "" && !strings.EqualFold(sha1Hash, hash.Value)) {
				return Artifact{}, fmt.Errorf("malformed CurseForge SHA1 hash for %d", file.ID)
			}
			sha1Hash = hash.Value
		case 2:
			if !modHash(hash.Value, 16) || (md5Hash != "" && !strings.EqualFold(md5Hash, hash.Value)) {
				return Artifact{}, fmt.Errorf("malformed CurseForge MD5 hash for %d", file.ID)
			}
			md5Hash = hash.Value
		}
	}
	algorithm, hash := "sha1", sha1Hash
	if hash == "" {
		algorithm, hash = "md5", md5Hash
	}
	if hash == "" {
		return Artifact{}, fmt.Errorf("CurseForge file %d has no supported hash", file.ID)
	}
	return Artifact{URL: file.DownloadURL.Value, Filename: file.Filename, Size: file.Size, HashAlgorithm: algorithm, Hash: hash}, nil
}

func (c *Client) resolveCurseForge(ctx context.Context, id, minecraft, loader string, allowPrerelease bool) (ModRelease, error) {
	numericID, err := curseForgeID(id)
	if err != nil {
		return ModRelease{}, err
	}
	project, err := c.curseForgeProject(ctx, numericID)
	if err != nil {
		return ModRelease{}, err
	}
	if project.AllowDistribution != nil && !*project.AllowDistribution {
		return ModRelease{}, fmt.Errorf("CurseForge author disabled distribution for %d: %w", numericID, ErrUnavailable)
	}
	loaderType, loaderTag := curseForgeLoader(loader)
	var best curseForgeFile
	var artifact Artifact
	for index := 0; ; {
		query := url.Values{"gameVersion": {minecraft}, "modLoaderType": {strconv.Itoa(loaderType)}, "index": {strconv.Itoa(index)}, "pageSize": {"50"}}
		var response struct {
			Data       []curseForgeFile `json:"data"`
			Pagination *struct {
				Index       *int `json:"index"`
				PageSize    *int `json:"pageSize"`
				ResultCount *int `json:"resultCount"`
				TotalCount  *int `json:"totalCount"`
			} `json:"pagination"`
		}
		if err := c.curseForgeJSON(ctx, http.MethodGet, "/mods/"+strconv.FormatUint(uint64(numericID), 10)+"/files?"+query.Encode(), nil, &response); err != nil {
			return ModRelease{}, modNotFound(err, "list CurseForge files")
		}
		page := response.Pagination
		if response.Data == nil || page == nil || page.Index == nil || page.PageSize == nil || page.ResultCount == nil || page.TotalCount == nil {
			return ModRelease{}, fmt.Errorf("malformed CurseForge file list or pagination")
		}
		count := len(response.Data)
		if *page.Index != index || *page.PageSize < 1 || *page.PageSize > 50 || count > *page.PageSize || *page.ResultCount != count || *page.TotalCount < index+count {
			return ModRelease{}, fmt.Errorf("inconsistent CurseForge pagination at index %d", index)
		}
		for _, file := range response.Data {
			if err := file.validate(numericID); err != nil {
				return ModRelease{}, err
			}
			if !modHasTag(file.GameVersions, minecraft) || !modHasTag(file.GameVersions, loaderTag) || (!allowPrerelease && file.ReleaseType != 1) {
				continue
			}
			candidate, err := curseForgeArtifact(file)
			if errors.Is(err, ErrUnavailable) {
				continue
			}
			if err != nil {
				return ModRelease{}, err
			}
			if best.ID == 0 || file.Date.After(best.Date) || (file.Date.Equal(best.Date) && file.ID > best.ID) {
				best, artifact = file, candidate
			}
		}
		index += count
		if index == *page.TotalCount {
			break
		}
		if count == 0 || index+50 > 10000 {
			return ModRelease{}, fmt.Errorf("CurseForge pagination cannot enumerate all files for %d", numericID)
		}
	}
	if best.ID == 0 {
		return ModRelease{}, fmt.Errorf("CurseForge project %d for %s/%s: %w", numericID, minecraft, loader, ErrUnavailable)
	}
	var dependencies []Dependency
	for _, dependency := range best.Dependencies {
		kind := ""
		switch dependency.RelationType {
		case 1, 2, 4, 6:
			continue
		case 3:
			kind = "required"
		case 5:
			kind = "incompatible"
		default:
			return ModRelease{}, fmt.Errorf("malformed CurseForge dependency relation %d", dependency.RelationType)
		}
		if dependency.ModID == 0 {
			return ModRelease{}, fmt.Errorf("CurseForge dependency has no project ID")
		}
		dependencies = append(dependencies, Dependency{ProjectID: strconv.FormatUint(uint64(dependency.ModID), 10), Kind: kind})
	}
	return ModRelease{ProjectID: strconv.FormatUint(uint64(project.ID), 10), VersionID: strconv.FormatUint(uint64(best.ID), 10), Version: best.Name, Artifact: artifact, Dependencies: dependencies,
		Published: best.Date, Channel: curseForgeChannel(best.ReleaseType)}, nil
}

func curseForgeChannel(releaseType int) string {
	switch releaseType {
	case 1:
		return "release"
	case 2:
		return "beta"
	case 3:
		return "alpha"
	}
	return ""
}
