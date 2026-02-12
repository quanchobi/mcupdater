package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

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
	Files           []struct {
		ID     string `json:"id"`
		Hashes struct {
			Sha1   string `json:"sha1"`
			Sha512 string `json:"sha512"`
		} `json:"hashes"`
		URL      string `json:"url"`
		Filename string `json:"filename"`
		Primary  bool   `json:"primary"`
		Size     int    `json:"size"`
		FileType any    `json:"file_type"`
	} `json:"files"`
	Dependencies []any `json:"dependencies"`
}

func getModrinthModInfo(slug string, loader string, mcVersion string) ([]ModInfo, error) {
	// Returns a slice of mods for the specified loader for the specified minecraft version
	baseURL := fmt.Sprintf("https://api.modrinth.com/v2/project/%s/version", slug)
	params := url.Values{}

	httpClient := http.Client{}

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

	req.Header.Set("User-Agent", "github_quanchobi/mcupdater/0.1 (contact@quanchobi.io)") // TODO: this should be an option in the user's config.

	resp, err := httpClient.Do(req)
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
