package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// captureTransport records the outgoing request and returns an empty
// version list, so we can assert on the exact query sent to Modrinth.
type captureTransport struct {
	req *http.Request
}

func (c *captureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.req = r
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("[]")),
		Header:     make(http.Header),
		Request:    r,
	}, nil
}

// Modrinth's /project/{id}/version endpoint filters on `game_versions`
// (plural). Unknown params such as `game_version` are silently ignored,
// which returns versions for every Minecraft release.
func TestGetModrinthModInfo_QueryParams(t *testing.T) {
	ct := &captureTransport{}
	client := &http.Client{Transport: ct}

	if _, err := getModrinthModInfo(client, "fabric-api", "fabric", "1.21.10"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ct.req == nil {
		t.Fatal("no request was sent")
	}

	q := ct.req.URL.Query()

	if q.Has("game_version") {
		t.Errorf("query uses singular %q, which Modrinth ignores", "game_version")
	}

	var gameVersions []string
	if err := json.Unmarshal([]byte(q.Get("game_versions")), &gameVersions); err != nil {
		t.Fatalf("game_versions is not a JSON array: %q (%v)", q.Get("game_versions"), err)
	}
	if len(gameVersions) != 1 || gameVersions[0] != "1.21.10" {
		t.Errorf("game_versions = %v, want [1.21.10]", gameVersions)
	}

	var loaders []string
	if err := json.Unmarshal([]byte(q.Get("loaders")), &loaders); err != nil {
		t.Fatalf("loaders is not a JSON array: %q (%v)", q.Get("loaders"), err)
	}
	if len(loaders) != 1 || loaders[0] != "fabric" {
		t.Errorf("loaders = %v, want [fabric]", loaders)
	}

	if got := ct.req.URL.Path; got != "/v2/project/fabric-api/version" {
		t.Errorf("path = %q, want /v2/project/fabric-api/version", got)
	}
}
