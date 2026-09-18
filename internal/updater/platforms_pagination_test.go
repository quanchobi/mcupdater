package updater

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type platformRoundTripper func(*http.Request) (*http.Response, error)

func (transport platformRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func platformJSONResponse(request *http.Request, value any) (*http.Response, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), Request: request}, nil
}

func TestCurseForgePaginationSelectsCompatibleDownload(t *testing.T) {
	file := func(id int, game, loader, date string, channel int, address any) map[string]any {
		return map[string]any{
			"id": id, "modId": 1, "gameId": 432, "isAvailable": true,
			"displayName": fmt.Sprintf("release %d", id), "fileName": "example.jar",
			"releaseType": channel, "fileDate": date, "fileLength": 10,
			"gameVersions": []string{game, loader}, "downloadUrl": address,
			"hashes": []any{map[string]any{"algo": 1, "value": strings.Repeat("a", 40)}},
		}
	}
	client := &Client{CurseForgeKey: "test-key", HTTP: &http.Client{Transport: platformRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "api.curseforge.com" || request.Header.Get("x-api-key") != "test-key" {
			return nil, fmt.Errorf("request is not an authenticated CurseForge call")
		}
		switch request.URL.Path {
		case "/v1/mods/1":
			return platformJSONResponse(request, map[string]any{"data": map[string]any{"id": 1, "gameId": 432, "classId": 6, "name": "Example", "allowModDistribution": true}})
		case "/v1/mods/1/files":
			query := request.URL.Query()
			if query.Get("gameVersion") != "1.21.1" || query.Get("modLoaderType") != "5" || query.Get("pageSize") != "50" {
				return nil, fmt.Errorf("unsupported file filter: %s", request.URL.RawQuery)
			}
			var files []map[string]any
			index := 0
			switch query.Get("index") {
			case "0":
				for id := 1; id <= 50; id++ {
					// A Fabric tag is not a Quilt tag, even if the API returns it.
					files = append(files, file(id, "1.21.1", "Fabric", "2026-01-01T00:00:00Z", 1, "https://example.com/wrong-loader.jar"))
				}
			case "50":
				index = 50
				files = []map[string]any{
					file(51, "1.21.1", "Quilt", "2024-01-01T00:00:00Z", 1, "https://example.com/stable.jar"),
					file(52, "1.21.1", "Quilt", "2025-01-01T00:00:00Z", 2, "https://example.com/beta.jar"),
					file(53, "1.21.1", "Quilt", "2025-02-01T00:00:00Z", 1, nil),
					file(54, "1.21", "Quilt", "2026-02-01T00:00:00Z", 1, "https://example.com/wrong-game.jar"),
				}
			default:
				return nil, fmt.Errorf("unexpected page index: %s", query.Get("index"))
			}
			return platformJSONResponse(request, map[string]any{"data": files, "pagination": map[string]any{"index": index, "pageSize": 50, "resultCount": len(files), "totalCount": 54}})
		default:
			return nil, fmt.Errorf("unexpected API path: %s", request.URL.Path)
		}
	})}}
	mod := Mod{Platform: "curseforge", ProjectID: "01"}
	stable, err := client.ResolveMod(context.Background(), mod, "1.21.1", "quilt", false)
	if err != nil {
		t.Fatal(err)
	}
	if stable.ProjectID != "1" || stable.VersionID != "51" || stable.Artifact.URL != "https://example.com/stable.jar" {
		t.Fatalf("selected incorrect stable release: %+v", stable)
	}
	prerelease, err := client.ResolveMod(context.Background(), mod, "1.21.1", "quilt", true)
	if err != nil {
		t.Fatal(err)
	}
	if prerelease.VersionID != "52" || prerelease.Artifact.URL != "https://example.com/beta.jar" {
		t.Fatalf("selected incorrect prerelease: %+v", prerelease)
	}
}
