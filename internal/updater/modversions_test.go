package updater

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClassifyModChange(t *testing.T) {
	jan, jun := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	release := ModRelease{Version: "2.0", Published: jun}
	cases := map[string]PlannedMod{
		"new":       {Release: release},
		"unknown":   {Release: release, Installed: &InstalledVersion{}},
		"same":      {Release: release, Installed: &InstalledVersion{Number: "2.0", Published: jun}},
		"update":    {Release: release, Installed: &InstalledVersion{Number: "1.0", Published: jan}},
		"DOWNGRADE": {Release: ModRelease{Version: "1.0", Published: jan}, Installed: &InstalledVersion{Number: "2.0-alpha", Published: jun}},
	}
	for want, pm := range cases {
		if got := classify(pm); got != want {
			t.Errorf("%s: got %s", want, got)
		}
	}
}

// versionFileFixture serves Modrinth version_file lookups for installed JARs,
// keyed by SHA-1, and counts them.
func versionFileFixture(t *testing.T, versions map[string][]modrinthVersion, installed map[string]modrinthVersion) (*Client, *int32) {
	t.Helper()
	client := plannerFixture(t, versions, 0)
	base := client.HTTP.Transport
	var lookups int32
	client.HTTP.Transport = planTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.modrinth.com" && strings.HasPrefix(r.URL.Path, "/v2/version_file/") {
			atomic.AddInt32(&lookups, 1)
			v, ok := installed[strings.TrimPrefix(r.URL.Path, "/v2/version_file/")]
			if !ok {
				return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			}
			data, _ := json.Marshal(v)
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(data))), ContentLength: int64(len(data)), Request: r}, nil
		}
		return base.RoundTrip(r)
	})
	return client, &lookups
}

func sha1Hex(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestPlanShowsDowngradeOfPrereleaseMod(t *testing.T) {
	root := t.TempDir()
	putFixture(t, root, "mods/alpha-0.9.jar", "installed alpha")
	target := fixtureVersion("alpha") // published 2025-01-01, release
	installed := modrinthVersion{ID: "alpha-v0", ProjectID: "alpha", Version: "0.9-alpha", ReleaseType: "alpha",
		Published: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)}
	client, lookups := versionFileFixture(t, map[string][]modrinthVersion{"alpha": {target}},
		map[string]modrinthVersion{sha1Hex("installed alpha"): installed})
	cfg := Config{ServerDir: root, Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "latest"},
		Mods: []Mod{{Platform: "modrinth", ProjectID: "alpha", File: "alpha-0.9.jar"}}}
	p, err := client.BuildPlan(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if *lookups != 1 {
		t.Fatalf("expected one version_file lookup, got %d", *lookups)
	}
	var out strings.Builder
	p.Print(&out)
	for _, want := range []string{
		"[DOWNGRADE] modrinth:alpha 0.9-alpha -> 1.0 [alpha.jar]",
		"installed version is an alpha release and allow_prerelease is false",
		"WARNING: 1 mod(s) would be downgraded.",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestPlanUsesStateVersionsWithoutLookups(t *testing.T) {
	root := t.TempDir()
	putFixture(t, root, "mods/a.jar", "installed")
	digest, _ := fileHash(root + "/mods/a.jar")
	state := State{Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "0.16.0", Layout: "installer"},
		Mods: map[string]InstalledMod{"modrinth:a": {File: "a.jar", Version: "a-v0", SHA256: digest, VersionNumber: "0.5", Channel: "release",
			Published: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}}, Files: map[string]string{}}
	data, _ := json.Marshal(state)
	putFixture(t, root, ".mcupdater/state.json", string(data))
	client, lookups := versionFileFixture(t, map[string][]modrinthVersion{"a": {fixtureVersion("a")}}, nil)
	cfg := Config{ServerDir: root, Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "latest"},
		Mods: []Mod{{Platform: "modrinth", ProjectID: "a"}}}
	p, err := client.BuildPlan(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if *lookups != 0 {
		t.Fatalf("state versions ignored: %d lookups", *lookups)
	}
	var out strings.Builder
	p.Print(&out)
	if !strings.Contains(out.String(), "[update]    modrinth:a 0.5 -> 1.0 [a.jar]") {
		t.Fatalf("update line missing:\n%s", out.String())
	}
}

func TestPlanMarksNewAndUnknownMods(t *testing.T) {
	root := t.TempDir()
	putFixture(t, root, "mods/b.jar", "unidentified bytes")
	client, _ := versionFileFixture(t, map[string][]modrinthVersion{"a": {fixtureVersion("a")}, "b": {fixtureVersion("b")}}, nil)
	cfg := Config{ServerDir: root, Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "latest"},
		Mods: []Mod{{Platform: "modrinth", ProjectID: "a"}, {Platform: "modrinth", ProjectID: "b", File: "b.jar"}}}
	p, err := client.BuildPlan(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	p.Print(&out)
	for _, want := range []string{"[new]       modrinth:a -> 1.0 [a.jar]", "[unknown]   modrinth:b ? -> 1.0 [b.jar]"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestApplyRecordsVersionDetails(t *testing.T) {
	root := t.TempDir()
	v := fixtureVersion("a")
	v.Files[0].Hashes = map[string]string{"sha1": sha1Hex("jar bytes")}
	v.Files[0].Size = int64(len("jar bytes"))
	client := plannerFixture(t, map[string][]modrinthVersion{"a": {v}}, 0)
	base := client.HTTP.Transport
	client.HTTP.Transport = planTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == "https://fixture.invalid/a.jar" {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("jar bytes")), ContentLength: 9, Request: r}, nil
		}
		return base.RoundTrip(r)
	})
	cfg := Config{ServerDir: root, Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "latest", Layout: "launcher", LauncherFile: "server.jar"},
		Mods: []Mod{{Platform: "modrinth", ProjectID: "a"}}}
	// Use the launcher layout so no Java installer is needed.
	f := newLauncherFixture(t)
	launcherBase := f.HTTP.Transport
	modBase := client.HTTP.Transport
	client.HTTP.Transport = planTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "api.modrinth.com" || r.URL.Host == "fixture.invalid" && r.URL.Path == "/a.jar" {
			return modBase.RoundTrip(r)
		}
		return launcherBase.RoundTrip(r)
	})
	release, err := Lock(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	p, err := client.BuildPlan(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Apply(context.Background(), cfg, p, io.Discard); err != nil {
		t.Fatal(err)
	}
	state, err := readState(root)
	if err != nil {
		t.Fatal(err)
	}
	got := state.Mods["modrinth:a"]
	if got.VersionNumber != "1.0" || got.Channel != "release" || !got.Published.Equal(v.Published) {
		t.Fatalf("version details not recorded: %+v", got)
	}
}
