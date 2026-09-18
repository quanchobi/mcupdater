package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCurseForgeAuthorDistributionOptOutIsUnavailable(t *testing.T) {
	client := &Client{CurseForgeKey: "test-key", HTTP: &http.Client{Transport: planTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/mods/123" {
			return nil, fmt.Errorf("must not bypass distribution opt-out: %s", r.URL)
		}
		body := `{"data":{"id":123,"gameId":432,"classId":6,"name":"Private","allowModDistribution":false}}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}}
	_, err := client.ResolveMod(context.Background(), Mod{Platform: "curseforge", ProjectID: "123"}, "1.21.1", "neoforge", false)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected unavailable without download bypass, got %v", err)
	}
}
