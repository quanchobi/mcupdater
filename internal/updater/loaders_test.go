package updater

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerUsesRepositoryChecksumNotStaleDiscoveryHash(t *testing.T) {
	client := plannerFixture(t, nil, 0)
	base := client.HTTP.Transport
	bytes := "published installer"
	sum := sha512.Sum512([]byte(bytes))
	client.HTTP.Transport = planTransport(func(r *http.Request) (*http.Response, error) {
		raw := ""
		switch {
		case r.URL.Host == "meta.fabricmc.net" && r.URL.Path == "/v2/versions/installer":
			data, _ := json.Marshal([]any{map[string]any{"version": "1.0.0", "stable": true, "url": "https://fixture.invalid/installer.jar", "hashes": map[string]string{"sha512": strings.Repeat("0", 128)}}})
			raw = string(data)
		case r.URL.Host == "fixture.invalid" && r.URL.Path == "/installer.jar.sha512":
			raw = hex.EncodeToString(sum[:])
		case r.URL.Host == "fixture.invalid" && r.URL.Path == "/installer.jar":
			raw = bytes
		default:
			return base.RoundTrip(r)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw)), ContentLength: int64(len(raw)), Request: r}, nil
	})
	release, err := client.ResolveServer(context.Background(), Config{Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "latest"}})
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "installer.jar")
	if err = client.Download(context.Background(), release.Installer, dest); err != nil {
		t.Fatalf("published installer rejected due to stale discovery hash: %v", err)
	}
	assertFile(t, filepath.Dir(dest), "installer.jar", bytes)
}

func TestLegacyForgeUniversalLauncher(t *testing.T) {
	root := t.TempDir()
	launcher := "minecraftforge-universal-1.6.4-9.11.1.1345.jar"
	putFixture(t, root, launcher, "legacy launcher")
	putFixture(t, root, "minecraft_server.1.6.4.jar", "vanilla server")
	putFixture(t, root, "forge-1.6.4-9.11.1.1345-installer.jar", "installer, not a launcher")
	putFixture(t, root, "libraries/dependency.jar", "library")
	command, err := installedForgeCommand(root, "java")
	if err != nil {
		t.Fatal(err)
	}
	if command != "java -jar "+launcher+" nogui" {
		t.Fatalf("wrong executable selected: %q", command)
	}
	putFixture(t, root, "forge-1.12.2-14.23.5.2860.jar", "another launcher")
	if _, err := installedForgeCommand(root, "java"); err == nil {
		t.Fatal("ambiguous legacy launcher selection was accepted")
	}
}
