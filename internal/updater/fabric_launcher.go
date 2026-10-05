package updater

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const (
	fabricLauncherMain   = "net.fabricmc.installer.ServerLauncher"
	fabricMetaLoaderURL  = "https://meta.fabricmc.net/v2/versions/loader/"
	fabricInstallerMaven = "https://maven.fabricmc.net/net/fabricmc/fabric-installer/"
)

var errNotFabricLauncher = errors.New("not a Fabric server launcher")

// fabricLauncherInfo is what a Fabric executable server launcher declares: the
// installer it was built from (manifest) and the loader/game it bootstraps
// (install.properties).
type fabricLauncherInfo struct {
	Installer, Loader, Minecraft string
}

func readZipEntry(f *zip.File, limit int64) ([]byte, error) {
	r, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = fmt.Errorf("ZIP entry %q exceeds %d bytes", f.Name, limit)
	}
	return data, err
}

func manifestAttributes(data []byte) map[string]string {
	attrs := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if key, value, ok := strings.Cut(line, ": "); ok && !strings.HasPrefix(line, " ") {
			attrs[key] = value
		}
	}
	return attrs
}

// parseInstallProperties accepts exactly the two keys Fabric meta writes, so a
// launcher cannot smuggle extra launcher configuration past verification.
func parseInstallProperties(data []byte) (map[string]string, error) {
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || (key != "fabric-loader-version" && key != "game-version") {
			return nil, fmt.Errorf("unexpected install.properties line %q", line)
		}
		if _, dup := values[key]; dup || !loaderVersionComponent.MatchString(value) {
			return nil, fmt.Errorf("invalid install.properties value for %s", key)
		}
		values[key] = value
	}
	if len(values) != 2 {
		return nil, fmt.Errorf("install.properties must set fabric-loader-version and game-version")
	}
	return values, nil
}

// inspectFabricLauncher reads what a launcher declares. It proves nothing about
// integrity; verifyFabricLauncher does that.
func inspectFabricLauncher(z *zip.Reader) (fabricLauncherInfo, error) {
	var manifest, props *zip.File
	for _, f := range z.File {
		if f.Name == "install.properties" {
			if props != nil {
				return fabricLauncherInfo{}, fmt.Errorf("duplicate ZIP entry %q", f.Name)
			}
			props = f
		}
		if f.Name == "META-INF/MANIFEST.MF" {
			if manifest != nil {
				return fabricLauncherInfo{}, fmt.Errorf("duplicate ZIP entry %q", f.Name)
			}
			manifest = f
		}
	}
	if manifest == nil || props == nil {
		return fabricLauncherInfo{}, errNotFabricLauncher
	}
	data, err := readZipEntry(manifest, 64<<10)
	if err != nil {
		return fabricLauncherInfo{}, err
	}
	attrs := manifestAttributes(data)
	if attrs["Main-Class"] != fabricLauncherMain || attrs["Implementation-Title"] != "FabricInstaller" {
		return fabricLauncherInfo{}, errNotFabricLauncher
	}
	if !validNumericLoaderVersion(attrs["Implementation-Version"]) {
		return fabricLauncherInfo{}, fmt.Errorf("Fabric launcher has an invalid installer version %q", attrs["Implementation-Version"])
	}
	data, err = readZipEntry(props, 4<<10)
	if err != nil {
		return fabricLauncherInfo{}, err
	}
	values, err := parseInstallProperties(data)
	if err != nil {
		return fabricLauncherInfo{}, err
	}
	return fabricLauncherInfo{Installer: attrs["Implementation-Version"], Loader: values["fabric-loader-version"], Minecraft: values["game-version"]}, nil
}

func zipDigests(z *zip.Reader, skip string) (map[string]string, error) {
	out := map[string]string{}
	var total int64
	for _, f := range z.File {
		if f.Name == skip {
			continue
		}
		if _, dup := out[f.Name]; dup {
			return nil, fmt.Errorf("duplicate ZIP entry %q", f.Name)
		}
		data, err := readZipEntry(f, 16<<20)
		if err != nil {
			return nil, err
		}
		if total += int64(len(data)); total > 64<<20 {
			return nil, fmt.Errorf("archive exceeds 64 MiB uncompressed")
		}
		sum := sha256.Sum256(data)
		out[f.Name] = hex.EncodeToString(sum[:])
	}
	return out, nil
}

// verifyFabricLauncher proves a launcher is exactly the checksummed Maven
// installer -server.jar plus an install.properties naming the expected versions.
// Fabric meta publishes no checksum for generated launchers; this is the substitute.
func verifyFabricLauncher(launcher, installer *zip.Reader, want fabricLauncherInfo) error {
	got, err := inspectFabricLauncher(launcher)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("launcher declares installer %s, loader %s, Minecraft %s; expected installer %s, loader %s, Minecraft %s",
			got.Installer, got.Loader, got.Minecraft, want.Installer, want.Loader, want.Minecraft)
	}
	have, err := zipDigests(launcher, "install.properties")
	if err != nil {
		return fmt.Errorf("launcher: %w", err)
	}
	expected, err := zipDigests(installer, "")
	if err != nil {
		return fmt.Errorf("installer: %w", err)
	}
	if _, ok := expected["install.properties"]; ok {
		return fmt.Errorf("Maven installer unexpectedly contains install.properties")
	}
	for name, sum := range expected {
		if have[name] != sum {
			return fmt.Errorf("launcher entry %q is missing or differs from the Maven installer", name)
		}
	}
	for name := range have {
		if _, ok := expected[name]; !ok {
			return fmt.Errorf("launcher contains entry %q that is not in the Maven installer", name)
		}
	}
	return nil
}

// verifyFabricLauncherFiles is the on-disk form used by staging.
func verifyFabricLauncherFiles(launcherPath, installerPath string, want fabricLauncherInfo) error {
	l, err := zip.OpenReader(launcherPath)
	if err != nil {
		return fmt.Errorf("open launcher: %w", err)
	}
	defer l.Close()
	i, err := zip.OpenReader(installerPath)
	if err != nil {
		return fmt.Errorf("open installer: %w", err)
	}
	defer i.Close()
	return verifyFabricLauncher(&l.Reader, &i.Reader, want)
}

// fabricInstallerServerArtifact resolves the checksummed installer -server.jar
// for an installer version from Fabric's Maven repository.
func (c *Client) fabricInstallerServerArtifact(ctx context.Context, version string) (Artifact, error) {
	if !validNumericLoaderVersion(version) {
		return Artifact{}, fmt.Errorf("invalid Fabric installer version %q", version)
	}
	v := url.PathEscape(version)
	artifact, err := c.resolveInstallerArtifact(ctx, Artifact{URL: fabricInstallerMaven + v + "/fabric-installer-" + v + "-server.jar", Filename: "fabric-installer-server.jar"})
	if err != nil {
		return Artifact{}, fmt.Errorf("Fabric installer %s server artifact: %w", version, err)
	}
	return artifact, nil
}

// resolveFabricLauncher selects the launcher layout's artifacts. The launcher
// itself has no published checksum and is verified after download.
func (c *Client) resolveFabricLauncher(ctx context.Context, release *ServerRelease, installer loaderMetaVersion) error {
	var err error
	release.InstallerVersion = installer.Version
	release.LauncherInstaller, err = c.fabricInstallerServerArtifact(ctx, installer.Version)
	if err != nil {
		return err
	}
	release.Launcher = Artifact{
		URL: fabricMetaLoaderURL + url.PathEscape(release.Minecraft) + "/" + url.PathEscape(release.Loader.Version) +
			"/" + url.PathEscape(installer.Version) + "/server/jar",
		Filename: release.Loader.LauncherFile,
	}
	return nil
}

// installFabricLauncher stages a verified launcher. Like vanilla it runs no Java:
// the launcher downloads vanilla and libraries into .fabric/ on first start.
func (c *Client) installFabricLauncher(ctx context.Context, release ServerRelease, dest, java string) (string, error) {
	if !safeJarName(release.Loader.LauncherFile) {
		return "", fmt.Errorf("unsafe launcher filename %q", release.Loader.LauncherFile)
	}
	work, err := os.MkdirTemp(dest, ".mcupdater-launcher-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	installer := filepath.Join(work, "installer-server.jar")
	if err = c.Download(ctx, release.LauncherInstaller, installer); err != nil {
		return "", fmt.Errorf("download Fabric installer: %w", err)
	}
	launcher := filepath.Join(work, "launcher.jar")
	if err = c.Download(ctx, release.Launcher, launcher); err != nil {
		return "", fmt.Errorf("download Fabric launcher: %w", err)
	}
	want := fabricLauncherInfo{Installer: release.InstallerVersion, Loader: release.Loader.Version, Minecraft: release.Minecraft}
	if err = verifyFabricLauncherFiles(launcher, installer, want); err != nil {
		return "", fmt.Errorf("verify Fabric launcher: %w", err)
	}
	if err = os.Rename(launcher, filepath.Join(dest, release.Loader.LauncherFile)); err != nil {
		return "", err
	}
	return loaderShellQuote(java) + " -jar " + loaderShellQuote(release.Loader.LauncherFile) + " nogui", nil
}
