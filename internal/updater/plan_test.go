package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type planTransport func(*http.Request) (*http.Response, error)

func (f planTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func plannerFixture(t *testing.T, versions map[string][]modrinthVersion, unavailableStatus int) *Client {
	t.Helper()
	return &Client{HTTP: &http.Client{Transport: planTransport(func(r *http.Request) (*http.Response, error) {
		var value any
		status := 200
		raw := ""
		switch {
		case r.URL.String() == minecraftManifestURL:
			value = map[string]any{"latest": map[string]string{"release": "1.21.1"}, "versions": []any{map[string]string{"id": "1.21.1", "type": "release", "url": "https://fixture.invalid/game"}}}
		case r.URL.Host == "fixture.invalid" && r.URL.Path == "/game":
			value = map[string]any{"id": "1.21.1", "javaVersion": map[string]int{"majorVersion": 21}, "downloads": map[string]any{"server": map[string]any{"url": "https://fixture.invalid/server.jar", "sha1": strings.Repeat("0", 40), "size": 1}}}
		case r.URL.Host == "meta.fabricmc.net" && r.URL.Path == "/v2/versions/game":
			value = []any{map[string]any{"version": "1.21.1", "stable": true}}
		case r.URL.Host == "meta.fabricmc.net" && strings.HasPrefix(r.URL.Path, "/v2/versions/loader/"):
			value = []any{map[string]any{"loader": map[string]any{"version": "0.16.0", "stable": true}}}
		case r.URL.Host == "meta.fabricmc.net" && r.URL.Path == "/v2/versions/installer":
			value = []any{map[string]any{"version": "1.0.0", "stable": true, "url": "https://fixture.invalid/installer.jar"}}
		case r.URL.Host == "fixture.invalid" && r.URL.Path == "/installer.jar" && r.Method == http.MethodHead:
			raw = "x"
		case r.URL.Host == "fixture.invalid" && r.URL.Path == "/installer.jar.sha1":
			raw = strings.Repeat("0", 40)
		case r.URL.Host == "fixture.invalid" && (r.URL.Path == "/installer.jar.sha512" || r.URL.Path == "/installer.jar.sha256"):
			status = http.StatusNotFound
		case r.URL.Host == "api.modrinth.com":
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(parts) < 3 {
				return nil, fmt.Errorf("unexpected request %s", r.URL)
			}
			id := parts[2]
			if len(parts) == 3 {
				value = map[string]string{"id": id, "title": id, "project_type": "mod", "server_side": "required"}
			} else {
				if unavailableStatus != 0 && id == "optional" {
					status = unavailableStatus
					raw = "failure"
				} else {
					value = versions[id]
					if value == nil || versions[id] == nil {
						value = []any{}
					}
				}
			}
		default:
			return nil, fmt.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		if value != nil {
			b, err := json.Marshal(value)
			if err != nil {
				return nil, err
			}
			raw = string(b)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw)), ContentLength: int64(len(raw)), Request: r}, nil
	})}}
}

func fixtureVersion(project string) modrinthVersion {
	return modrinthVersion{ID: project + "-v1", ProjectID: project, Version: "1.0", Published: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), ReleaseType: "release", GameVersions: []string{"1.21.1"}, Loaders: []string{"fabric"}, Files: []modrinthFile{{Filename: project + ".jar", URL: "https://fixture.invalid/" + project + ".jar", Size: 1, Primary: true, Hashes: map[string]string{"sha512": strings.Repeat("0", 128)}}}}
}

func TestMandatoryMissingBlocksButOptionalMissingCanProceed(t *testing.T) {
	root := t.TempDir()
	putFixture(t, root, "mods/old.jar", "installed optional")
	required := true
	cfg := Config{ServerDir: root, Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "latest"}, Mods: []Mod{{Platform: "modrinth", ProjectID: "optional", File: "old.jar", Mandatory: &required}}}
	client := plannerFixture(t, nil, 0)
	if _, err := client.BuildPlan(context.Background(), cfg); err == nil {
		t.Fatal("mandatory missing mod did not block")
	}
	assertFile(t, root, "mods/old.jar", "installed optional")
	required = false
	p, err := client.BuildPlan(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Mods) != 1 || p.Mods[0].Missing == "" || !p.Changed {
		t.Fatalf("optional missing mod was not marked for omission: %+v", p)
	}
	assertFile(t, root, "mods/old.jar", "installed optional")
}

func TestOptionalModAPIFailureIsFatal(t *testing.T) {
	optional := false
	cfg := Config{ServerDir: t.TempDir(), Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "latest"}, Mods: []Mod{{Platform: "modrinth", ProjectID: "optional", Mandatory: &optional}}}
	if _, err := plannerFixture(t, nil, http.StatusTooManyRequests).BuildPlan(context.Background(), cfg); err == nil {
		t.Fatal("API rate limit was treated as optional mod incompatibility")
	}
}

func TestRequiredDependencyLossPropagatesToMandatoryMod(t *testing.T) {
	parent := fixtureVersion("required")
	// Parse dependency metadata as it arrives from the platform.
	if err := json.Unmarshal([]byte(`[{"project_id":"optional","dependency_type":"required"}]`), &parent.Dependencies); err != nil {
		t.Fatal(err)
	}
	optional := false
	cfg := Config{ServerDir: t.TempDir(), Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "latest"}, Mods: []Mod{{Platform: "modrinth", ProjectID: "required"}, {Platform: "modrinth", ProjectID: "optional", Mandatory: &optional}}}
	if _, err := plannerFixture(t, map[string][]modrinthVersion{"required": {parent}}, 0).BuildPlan(context.Background(), cfg); err == nil {
		t.Fatal("mandatory mod with missing optional-marked dependency was accepted")
	}
	dependency := fixtureVersion("optional")
	if _, err := plannerFixture(t, map[string][]modrinthVersion{"required": {parent}, "optional": {dependency}}, 0).BuildPlan(context.Background(), cfg); err != nil {
		t.Fatalf("compatible configured dependency rejected: %v", err)
	}
}

func TestPreviouslyOmittedModCanBeInventoriedAgain(t *testing.T) {
	root := t.TempDir()
	putFixture(t, root, "mods/restored.jar", "manually restored old mod")
	putFixture(t, root, ".mcupdater/state.json", `{"minecraft":"1.21.1","loader":{"kind":"fabric","version":"0.16.0"},"mods":{"modrinth:optional":{"file":"","version":"","sha256":""}},"files":{},"start":""}`)
	optional := false
	cfg := Config{ServerDir: root, Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "latest"}, Mods: []Mod{{Platform: "modrinth", ProjectID: "optional", File: "restored.jar", Mandatory: &optional}}}
	p, err := plannerFixture(t, nil, 0).BuildPlan(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Changed || p.Mods[0].Missing == "" {
		t.Fatal("restored incompatible mod was left installed instead of planned for omission")
	}
}

func TestConflictsCheckedAfterTransitiveOptionalOmissions(t *testing.T) {
	required, optional, leaf := fixtureVersion("required"), fixtureVersion("optional"), fixtureVersion("leaf")
	for _, dependency := range []struct {
		version *modrinthVersion
		data    string
	}{
		{&required, `[{"project_id":"optional","dependency_type":"incompatible"}]`},
		{&optional, `[{"project_id":"leaf","dependency_type":"required"}]`},
		{&leaf, `[{"project_id":"absent","dependency_type":"required"}]`},
	} {
		if err := json.Unmarshal([]byte(dependency.data), &dependency.version.Dependencies); err != nil {
			t.Fatal(err)
		}
	}
	no := false
	mods := []Mod{{Platform: "modrinth", ProjectID: "required"}, {Platform: "modrinth", ProjectID: "optional", Mandatory: &no}, {Platform: "modrinth", ProjectID: "leaf", Mandatory: &no}}
	versions := map[string][]modrinthVersion{"required": {required}, "optional": {optional}, "leaf": {leaf}}
	for _, order := range [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}} {
		t.Run(fmt.Sprint(order), func(t *testing.T) {
			cfg := Config{ServerDir: t.TempDir(), Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "latest"}, Mods: []Mod{mods[order[0]], mods[order[1]], mods[order[2]]}}
			p, err := plannerFixture(t, versions, 0).BuildPlan(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, mod := range p.Mods {
				if (mod.Missing == "") != mod.Mod.Required() {
					t.Fatalf("only the mandatory mod should survive: %+v", mod)
				}
			}
		})
	}
	// Restoring the optional chain leaves a real conflict, which must still block.
	leaf.Dependencies = nil
	versions["leaf"] = []modrinthVersion{leaf}
	cfg := Config{ServerDir: t.TempDir(), Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "latest"}, Mods: mods}
	if _, err := plannerFixture(t, versions, 0).BuildPlan(context.Background(), cfg); err == nil {
		t.Fatal("surviving incompatible mods were accepted")
	}
}

func TestPlanUsesExplicitModrinthEnvironments(t *testing.T) {
	for _, test := range []struct {
		name         string
		environments []string
		legacySide   string
		available    bool
	}{
		{"mixed project", []string{"client_only", "server_only"}, "unsupported", true},
		{"client-only project", []string{"client_only", "singleplayer_only"}, "required", false},
		{"legacy client-only project", nil, "unsupported", false},
		{"legacy server project", nil, "required", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := fixtureVersion("mixed")
			server.Environment = "server_only"
			clientOnly := fixtureVersion("mixed")
			clientOnly.ID, clientOnly.Environment = "client-v2", "client_only"
			clientOnly.Published = server.Published.Add(time.Hour)
			client := plannerFixture(t, map[string][]modrinthVersion{"mixed": {clientOnly, server}}, 0)
			base := client.HTTP.Transport
			data, err := json.Marshal(modrinthProject{ID: "mixed", Title: "mixed", ProjectType: "mod", ServerSide: test.legacySide, Environment: test.environments})
			if err != nil {
				t.Fatal(err)
			}
			client.HTTP.Transport = planTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "api.modrinth.com" && r.URL.Path == "/v2/project/mixed" {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(data))), Request: r}, nil
				}
				return base.RoundTrip(r)
			})
			no := false
			root := t.TempDir()
			putFixture(t, root, "mods/mixed.jar", "installed server mod")
			cfg := Config{ServerDir: root, Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "latest"}, Mods: []Mod{{Platform: "modrinth", ProjectID: "mixed", File: "mixed.jar", Mandatory: &no}}}
			p, err := client.BuildPlan(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			mod := p.Mods[0]
			if (mod.Missing == "") != test.available || (test.available && mod.Release.VersionID != server.ID) {
				t.Fatalf("incorrect server mod selection: %+v", mod)
			}
		})
	}
}
