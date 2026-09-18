package updater

import (
	"archive/zip"
	"context"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const minecraftManifestURL = "https://piston-meta.mojang.com/mc/game/version_manifest_v2.json"
const forgeMavenURL = "https://maven.minecraftforge.net/net/minecraftforge/forge/"
const neoForgeMavenURL = "https://maven.neoforged.net/releases/net/neoforged/"

var loaderVersionComponent = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]{0,127}$`)

type loaderMetaVersion struct {
	Version string `json:"version"`
	Stable  *bool  `json:"stable"`
	URL     string `json:"url"`
	Size    int64  `json:"file_size"`
}

// ResolveServer fixes the game first, independently of loader support. It never
// silently downgrades Minecraft to find a compatible loader.
func (c *Client) ResolveServer(ctx context.Context, cfg Config) (ServerRelease, error) {
	release, err := c.resolveMinecraft(ctx, cfg.Minecraft, cfg.AllowPrerelease)
	if err != nil {
		return ServerRelease{}, err
	}
	release.Loader = cfg.Loader
	switch cfg.Loader.Kind {
	case "vanilla":
		release.Loader.Version = ""
	case "fabric", "quilt":
		err = c.resolveMetaLoader(ctx, &release, cfg.AllowPrerelease)
	case "forge":
		err = c.resolveForge(ctx, &release, cfg.AllowPrerelease)
	case "neoforge":
		err = c.resolveNeoForge(ctx, &release, cfg.AllowPrerelease)
	default:
		err = fmt.Errorf("unsupported loader %q", cfg.Loader.Kind)
	}
	if err != nil {
		return ServerRelease{}, fmt.Errorf("resolve %s for Minecraft %s: %w", cfg.Loader.Kind, release.Minecraft, err)
	}
	return release, nil
}

func (c *Client) resolveMinecraft(ctx context.Context, requested string, prerelease bool) (ServerRelease, error) {
	var manifest struct {
		Latest struct {
			Release  string `json:"release"`
			Snapshot string `json:"snapshot"`
		} `json:"latest"`
		Versions []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"versions"`
	}
	if err := c.GetJSON(ctx, minecraftManifestURL, &manifest); err != nil {
		return ServerRelease{}, fmt.Errorf("Minecraft manifest: %w", err)
	}
	if len(manifest.Versions) == 0 {
		return ServerRelease{}, fmt.Errorf("Minecraft manifest has no versions")
	}
	latest := requested == "" || requested == "latest"
	if latest {
		requested = manifest.Latest.Release
		if prerelease {
			requested = manifest.Latest.Snapshot
		}
		if requested == "" {
			return ServerRelease{}, fmt.Errorf("Minecraft manifest has no latest version")
		}
	}
	for _, version := range manifest.Versions {
		if version.ID != requested {
			continue
		}
		if !loaderVersionComponent.MatchString(version.ID) || version.URL == "" || version.Type == "" {
			return ServerRelease{}, fmt.Errorf("malformed Minecraft version entry %q", requested)
		}
		if latest && !prerelease && version.Type != "release" {
			return ServerRelease{}, fmt.Errorf("Minecraft manifest latest release is not stable")
		}
		var metadata struct {
			ID        string `json:"id"`
			Downloads map[string]struct {
				URL  string `json:"url"`
				SHA1 string `json:"sha1"`
				Size int64  `json:"size"`
			} `json:"downloads"`
			JavaVersion struct {
				MajorVersion int `json:"majorVersion"`
			} `json:"javaVersion"`
		}
		if err := c.GetJSON(ctx, version.URL, &metadata); err != nil {
			return ServerRelease{}, fmt.Errorf("Minecraft %s metadata: %w", requested, err)
		}
		if metadata.ID != requested || metadata.Downloads == nil {
			return ServerRelease{}, fmt.Errorf("malformed Minecraft %s metadata", requested)
		}
		server, ok := metadata.Downloads["server"]
		if !ok {
			return ServerRelease{}, fmt.Errorf("Minecraft %s has no dedicated server: %w", requested, ErrUnavailable)
		}
		if server.URL == "" || server.Size <= 0 || !validLoaderDigest(server.SHA1, 20) {
			return ServerRelease{}, fmt.Errorf("malformed Minecraft %s server artifact", requested)
		}
		javaMajor := metadata.JavaVersion.MajorVersion
		if javaMajor == 0 {
			javaMajor = 8
		} // Legacy Mojang metadata predates javaVersion.
		return ServerRelease{Minecraft: requested, Vanilla: Artifact{URL: server.URL, Filename: "server.jar", HashAlgorithm: "sha1", Hash: server.SHA1, Size: server.Size}, JavaMajor: javaMajor}, nil
	}
	if latest {
		return ServerRelease{}, fmt.Errorf("Minecraft manifest latest version %q is missing from versions", requested)
	}
	return ServerRelease{}, fmt.Errorf("Minecraft %s: %w", requested, ErrUnavailable)
}

func (c *Client) resolveMetaLoader(ctx context.Context, release *ServerRelease, prerelease bool) error {
	base := "https://meta.fabricmc.net/v2/versions/"
	if release.Loader.Kind == "quilt" {
		base = "https://meta.quiltmc.org/v3/versions/"
	}
	var games []loaderMetaVersion
	if err := c.GetJSON(ctx, base+"game", &games); err != nil {
		return fmt.Errorf("supported games: %w", err)
	}
	if games == nil {
		return fmt.Errorf("malformed supported-game metadata")
	}
	supported := false
	for _, game := range games {
		if game.Version == "" {
			return fmt.Errorf("game metadata contains an empty version")
		}
		if game.Version == release.Minecraft {
			supported = true
		}
	}
	if !supported {
		return fmt.Errorf("game %s: %w", release.Minecraft, ErrUnavailable)
	}
	var loaders []struct {
		Loader       loaderMetaVersion `json:"loader"`
		LauncherMeta struct {
			MinJavaVersion int `json:"min_java_version"`
		} `json:"launcherMeta"`
	}
	if err := c.GetJSON(ctx, base+"loader/"+url.PathEscape(release.Minecraft), &loaders); err != nil {
		return fmt.Errorf("loader metadata: %w", err)
	}
	if loaders == nil {
		return fmt.Errorf("malformed loader metadata")
	}
	requested := release.Loader.Version
	latest := requested == "" || requested == "latest"
	selected := -1
	for i, candidate := range loaders {
		if !validNumericLoaderVersion(candidate.Loader.Version) {
			return fmt.Errorf("invalid loader version in metadata: %q", candidate.Loader.Version)
		}
		if latest {
			stable := !strings.Contains(candidate.Loader.Version, "-")
			if release.Loader.Kind == "fabric" {
				if candidate.Loader.Stable == nil {
					return fmt.Errorf("Fabric metadata is missing stability information")
				}
				stable = *candidate.Loader.Stable
			}
			if !prerelease && !stable {
				continue
			}
			if selected < 0 || compareLoaderVersions(candidate.Loader.Version, loaders[selected].Loader.Version) > 0 {
				selected = i
			}
		} else if candidate.Loader.Version == requested {
			selected = i
			break
		}
	}
	if selected < 0 {
		return fmt.Errorf("loader %s: %w", requested, ErrUnavailable)
	}
	release.Loader.Version = loaders[selected].Loader.Version
	if loaders[selected].LauncherMeta.MinJavaVersion > release.JavaMajor {
		release.JavaMajor = loaders[selected].LauncherMeta.MinJavaVersion
	}
	var installers []loaderMetaVersion
	if err := c.GetJSON(ctx, base+"installer", &installers); err != nil {
		return fmt.Errorf("installer metadata: %w", err)
	}
	if len(installers) == 0 {
		return fmt.Errorf("installer metadata has no releases")
	}
	selected = -1
	for i, candidate := range installers {
		if !validNumericLoaderVersion(candidate.Version) || candidate.URL == "" {
			return fmt.Errorf("malformed installer metadata")
		}
		stable := !strings.Contains(candidate.Version, "-")
		if release.Loader.Kind == "fabric" {
			if candidate.Stable == nil {
				return fmt.Errorf("Fabric installer metadata is missing stability information")
			}
			stable = *candidate.Stable
		}
		if !prerelease && !stable {
			continue
		}
		if selected < 0 || compareLoaderVersions(candidate.Version, installers[selected].Version) > 0 {
			selected = i
		}
	}
	if selected < 0 {
		return fmt.Errorf("no stable installer: %w", ErrUnavailable)
	}
	installer := installers[selected]
	artifact := Artifact{URL: installer.URL, Filename: release.Loader.Kind + "-installer.jar", Size: installer.Size}
	var err error
	release.Installer, err = c.resolveInstallerArtifact(ctx, artifact)
	// Current Quilt installers require Java 17 even for games targeting Java 8.
	if release.Loader.Kind == "quilt" && release.JavaMajor < 17 {
		release.JavaMajor = 17
	}
	return err
}

func (c *Client) mavenLoaderVersions(ctx context.Context, base string) ([]string, error) {
	data, err := c.Get(ctx, base+"maven-metadata.xml")
	if err != nil {
		return nil, fmt.Errorf("Maven metadata: %w", err)
	}
	var metadata struct {
		XMLName  xml.Name `xml:"metadata"`
		Versions []string `xml:"versioning>versions>version"`
	}
	if err := xml.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("malformed Maven metadata: %w", err)
	}
	if len(metadata.Versions) == 0 {
		return nil, fmt.Errorf("Maven metadata has no versions")
	}
	for _, version := range metadata.Versions {
		if !loaderVersionComponent.MatchString(version) {
			return nil, fmt.Errorf("unsafe Maven version %q", version)
		}
	}
	return metadata.Versions, nil
}

func (c *Client) resolveForge(ctx context.Context, release *ServerRelease, prerelease bool) error {
	versions, err := c.mavenLoaderVersions(ctx, forgeMavenURL)
	if err != nil {
		return err
	}
	requested := release.Loader.Version
	if requested == "" || requested == "latest" {
		var promotions struct {
			Promos map[string]string `json:"promos"`
		}
		if err := c.GetJSON(ctx, "https://files.minecraftforge.net/net/minecraftforge/forge/promotions_slim.json", &promotions); err != nil {
			return fmt.Errorf("Forge promotions: %w", err)
		}
		if promotions.Promos == nil {
			return fmt.Errorf("malformed Forge promotions")
		}
		channel := "recommended"
		if prerelease {
			channel = "latest"
		}
		requested = promotions.Promos[release.Minecraft+"-"+channel]
		if requested == "" {
			return fmt.Errorf("no Forge %s release for %s: %w", channel, release.Minecraft, ErrUnavailable)
		}
	}
	coordinate := ""
	for _, candidate := range versions {
		suffix, ok := strings.CutPrefix(candidate, release.Minecraft+"-")
		if !ok {
			continue
		}
		// Older Forge coordinates can repeat the game version as a branch suffix.
		short := strings.TrimSuffix(suffix, "-"+release.Minecraft)
		if requested == candidate || requested == suffix || requested == short {
			coordinate = candidate
			if suffix == short {
				break
			}
		}
	}
	if coordinate == "" {
		return fmt.Errorf("Forge %s: %w", requested, ErrUnavailable)
	}
	release.Loader.Version = strings.TrimPrefix(coordinate, release.Minecraft+"-")
	release.Installer, err = c.resolveInstallerArtifact(ctx, Artifact{
		URL:      forgeMavenURL + coordinate + "/forge-" + coordinate + "-installer.jar",
		Filename: "forge-" + coordinate + "-installer.jar",
	})
	return err
}

func (c *Client) resolveNeoForge(ctx context.Context, release *ServerRelease, prerelease bool) error {
	artifactName, prefix := "neoforge", ""
	legacy := release.Minecraft == "1.20.1"
	if legacy {
		artifactName, prefix = "forge", "1.20.1-"
	} else {
		var ok bool
		prefix, ok = neoForgeGamePrefix(release.Minecraft)
		if !ok {
			return fmt.Errorf("NeoForge game %s: %w", release.Minecraft, ErrUnavailable)
		}
	}
	base := neoForgeMavenURL + artifactName + "/"
	versions, err := c.mavenLoaderVersions(ctx, base)
	if err != nil {
		return err
	}
	requested := release.Loader.Version
	latest := requested == "" || requested == "latest"
	var candidates []string
	for _, candidate := range versions {
		if !strings.HasPrefix(candidate, prefix) {
			continue
		}
		version := candidate
		if legacy {
			version = strings.TrimPrefix(candidate, prefix)
		}
		if !validNumericLoaderVersion(version) {
			return fmt.Errorf("malformed NeoForge version %q", candidate)
		}
		if latest {
			if !prerelease && strings.Contains(version, "-") {
				continue
			}
			candidates = append(candidates, candidate)
		} else if requested == candidate || requested == version {
			candidates = append(candidates, candidate)
			break
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if legacy {
			a, b = strings.TrimPrefix(a, prefix), strings.TrimPrefix(b, prefix)
		}
		return compareLoaderVersions(a, b) > 0
	})
	selected := ""
	for _, candidate := range candidates {
		// The 26.x branch also publishes snapshot builds under the final game's
		// numeric prefix. NeoForm, not that prefix, identifies their actual game.
		if !legacy && !strings.HasPrefix(release.Minecraft, "1.") {
			game, err := c.neoForgeTargetGame(ctx, base, candidate)
			if err != nil {
				return err
			}
			if game != release.Minecraft {
				continue
			}
		}
		selected = candidate
		break
	}
	if selected == "" {
		return fmt.Errorf("NeoForge %s for %s: %w", requested, release.Minecraft, ErrUnavailable)
	}
	release.Loader.Version = selected
	if legacy {
		release.Loader.Version = strings.TrimPrefix(selected, prefix)
	}
	release.Installer, err = c.resolveInstallerArtifact(ctx, Artifact{URL: base + selected + "/" + artifactName + "-" + selected + "-installer.jar", Filename: artifactName + "-" + selected + "-installer.jar"})
	return err
}

func (c *Client) neoForgeTargetGame(ctx context.Context, base, version string) (string, error) {
	data, err := c.Get(ctx, base+version+"/neoforge-"+version+".pom")
	if err != nil {
		return "", fmt.Errorf("NeoForge game mapping: %w", err)
	}
	var project struct {
		XMLName      xml.Name `xml:"project"`
		Dependencies []struct {
			Group    string `xml:"groupId"`
			Artifact string `xml:"artifactId"`
			Version  string `xml:"version"`
		} `xml:"dependencies>dependency"`
	}
	if err := xml.Unmarshal(data, &project); err != nil {
		return "", fmt.Errorf("malformed NeoForge game mapping: %w", err)
	}
	for _, dependency := range project.Dependencies {
		if dependency.Group != "net.neoforged" || dependency.Artifact != "neoform" {
			continue
		}
		separator := strings.LastIndexByte(dependency.Version, '-')
		if separator <= 0 || separator == len(dependency.Version)-1 {
			return "", fmt.Errorf("malformed NeoForm version %q", dependency.Version)
		}
		return dependency.Version[:separator], nil
	}
	return "", fmt.Errorf("NeoForge metadata has no NeoForm game mapping")
}

// NeoForge switched from minor.patch.build to year.drop.patch.build with MC 26.1.
// 1.20.1 predates both schemes and is handled using net.neoforged:forge above.
func neoForgeGamePrefix(game string) (string, bool) {
	if strings.HasPrefix(game, "1.") && strings.Contains(game, "-") {
		return "", false
	}
	game, _, _ = strings.Cut(game, "-")
	parts := strings.Split(game, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return "", false
	}
	numbers := [3]int{}
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || strconv.Itoa(value) != part {
			return "", false
		}
		numbers[i] = value
	}
	if numbers[0] == 1 && numbers[1] >= 20 {
		return fmt.Sprintf("%d.%d.", numbers[1], numbers[2]), true
	}
	if numbers[0] >= 26 {
		return fmt.Sprintf("%d.%d.%d.", numbers[0], numbers[1], numbers[2]), true
	}
	return "", false
}

func (c *Client) resolveInstallerArtifact(ctx context.Context, artifact Artifact) (Artifact, error) {
	// Check the artifact, not its checksum sidecar: a missing sidecar is not proof
	// that a release is unavailable, and must not be silently ignored.
	response, err := c.request(ctx, http.MethodHead, artifact.URL, nil, nil)
	var status *HTTPError
	if errors.As(err, &status) && status.StatusCode == http.StatusMethodNotAllowed {
		response, err = c.request(ctx, http.MethodGet, artifact.URL, nil, nil)
	}
	if err != nil {
		if errors.As(err, &status) && status.StatusCode == http.StatusNotFound {
			return Artifact{}, fmt.Errorf("installer %s: %w", artifact.URL, ErrUnavailable)
		}
		return Artifact{}, fmt.Errorf("installer availability: %w", err)
	}
	response.Body.Close()
	if artifact.Size > 0 && response.ContentLength > 0 && artifact.Size != response.ContentLength {
		return Artifact{}, fmt.Errorf("installer size differs from published metadata")
	}
	if response.ContentLength > 0 {
		artifact.Size = response.ContentLength
	}
	// Discovery indexes can cache obsolete digests. The checksum published next
	// to the selected Maven artifact is authoritative; never retry a mismatching
	// downloaded artifact with a weaker checksum.
	for _, checksum := range []struct {
		name string
		size int
	}{{"sha512", 64}, {"sha256", 32}, {"sha1", 20}} {
		data, err := c.Get(ctx, artifact.URL+"."+checksum.name)
		if errors.As(err, &status) && status.StatusCode == http.StatusNotFound {
			continue
		}
		if err != nil {
			return Artifact{}, fmt.Errorf("published installer checksum: %w", err)
		}
		value := strings.TrimSpace(string(data))
		if !validLoaderDigest(value, checksum.size) {
			return Artifact{}, fmt.Errorf("malformed published installer %s checksum", checksum.name)
		}
		artifact.HashAlgorithm, artifact.Hash = checksum.name, value
		return artifact, nil
	}
	return Artifact{}, fmt.Errorf("installer has no published supported checksum")
}

func validLoaderDigest(value string, size int) bool {
	if len(value) != size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validNumericLoaderVersion(version string) bool {
	if !loaderVersionComponent.MatchString(version) {
		return false
	}
	core, _, _ := strings.Cut(version, "+")
	core, _, _ = strings.Cut(core, "-")
	for _, part := range strings.Split(core, ".") {
		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			return false
		}
	}
	return true
}

// Numeric core and prerelease identifiers are compared numerically (10 > 9).
// Build metadata does not affect precedence; a final release beats its prereleases.
func compareLoaderVersions(a, b string) int {
	a, _, _ = strings.Cut(a, "+")
	b, _, _ = strings.Cut(b, "+")
	ac, ap, _ := strings.Cut(a, "-")
	bc, bp, _ := strings.Cut(b, "-")
	aa, bb := strings.Split(ac, "."), strings.Split(bc, ".")
	for i := 0; i < len(aa) || i < len(bb); i++ {
		var av, bv uint64
		if i < len(aa) {
			av, _ = strconv.ParseUint(aa[i], 10, 64)
		}
		if i < len(bb) {
			bv, _ = strconv.ParseUint(bb[i], 10, 64)
		}
		if av > bv {
			return 1
		}
		if av < bv {
			return -1
		}
	}
	if ap == bp {
		return 0
	}
	if ap == "" {
		return 1
	}
	if bp == "" {
		return -1
	}
	aa, bb = strings.Split(ap, "."), strings.Split(bp, ".")
	for i := 0; i < len(aa) && i < len(bb); i++ {
		av, ae := strconv.ParseUint(aa[i], 10, 64)
		bv, be := strconv.ParseUint(bb[i], 10, 64)
		if ae == nil && be == nil {
			if av > bv {
				return 1
			}
			if av < bv {
				return -1
			}
		} else if ae == nil {
			return -1
		} else if be == nil {
			return 1
		} else if cmp := strings.Compare(aa[i], bb[i]); cmp != 0 {
			return cmp
		}
	}
	if len(aa) > len(bb) {
		return 1
	}
	if len(aa) < len(bb) {
		return -1
	}
	return 0
}

// InstallServer operates only on a fresh staging directory. It neither starts a
// server nor accepts the EULA. Official installers own their library layout.
func (c *Client) InstallServer(ctx context.Context, release ServerRelease, dest, java string, out io.Writer) (command string, err error) {
	if java == "" {
		java = "java"
	}
	if strings.ContainsAny(java, `/\`) && !filepath.IsAbs(java) {
		java, err = filepath.Abs(java)
		if err != nil {
			return "", err
		}
	}
	if out == nil {
		out = io.Discard
	}
	if !loaderVersionComponent.MatchString(release.Minecraft) || (release.Loader.Kind != "vanilla" && !loaderVersionComponent.MatchString(release.Loader.Version)) {
		return "", fmt.Errorf("invalid resolved server version")
	}
	if release.Loader.Kind != "vanilla" && release.Loader.Kind != "fabric" && release.Loader.Kind != "quilt" && release.Loader.Kind != "forge" && release.Loader.Kind != "neoforge" {
		return "", fmt.Errorf("unsupported loader %q", release.Loader.Kind)
	}
	dest, err = filepath.Abs(dest)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(dest, 0755); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		return "", err
	}
	if len(entries) != 0 {
		return "", fmt.Errorf("server staging directory must be empty")
	}
	info, err := os.Lstat(dest)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("server staging path must be a real directory")
	}
	if release.Loader.Kind == "vanilla" {
		if err = c.Download(ctx, release.Vanilla, filepath.Join(dest, "server.jar")); err != nil {
			return "", fmt.Errorf("download Minecraft server: %w", err)
		}
		return loaderShellQuote(java) + " -jar server.jar nogui", nil
	}
	work, err := os.MkdirTemp(dest, ".mcupdater-installer-")
	if err != nil {
		return "", err
	}
	defer func() {
		cleanupErr := os.RemoveAll(work)
		// Forge writes its installer log in the working directory, not beside the jar.
		for _, name := range []string{"installer.jar.log", "installer.log"} {
			if e := os.Remove(filepath.Join(dest, name)); e != nil && !errors.Is(e, os.ErrNotExist) {
				cleanupErr = errors.Join(cleanupErr, e)
			}
		}
		if cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove installer scratch files: %w", cleanupErr))
		}
	}()
	installer := filepath.Join(work, "installer.jar")
	if err = c.Download(ctx, release.Installer, installer); err != nil {
		return "", fmt.Errorf("download %s installer: %w", release.Loader.Kind, err)
	}
	vanillaPath := filepath.Join(dest, "server.jar")
	if release.Loader.Kind == "forge" || release.Loader.Kind == "neoforge" {
		vanillaPath, err = forgeVanillaPath(installer, dest, release.Minecraft)
		if err != nil {
			return "", err
		}
	}
	if err = c.Download(ctx, release.Vanilla, vanillaPath); err != nil {
		return "", fmt.Errorf("download Minecraft server: %w", err)
	}
	// Keep Java temporary files and incidental home-directory caches in staging.
	args := []string{"-Djava.awt.headless=true", "-Djava.io.tmpdir=" + work, "-Duser.home=" + work, "-jar", installer}
	switch release.Loader.Kind {
	case "fabric":
		args = append(args, "server", "-dir", ".", "-mcversion", release.Minecraft, "-loader", release.Loader.Version)
	case "quilt":
		// Quilt reparses joined CLI arguments and requires literal quotes around the
		// --install-dir value. A relative dot avoids its whitespace/path quoting trap.
		args = append(args, "install", "server", release.Minecraft, release.Loader.Version, `--install-dir="."`)
	case "forge", "neoforge":
		args = append(args, "--installServer")
	}
	process := exec.CommandContext(ctx, java, args...)
	process.Dir, process.Stdout, process.Stderr = dest, out, out
	process.WaitDelay = 5 * time.Second
	if err = process.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("%s installer: %w", release.Loader.Kind, ctx.Err())
		}
		return "", fmt.Errorf("%s installer failed: %w", release.Loader.Kind, err)
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if release.Loader.Kind == "fabric" || release.Loader.Kind == "quilt" {
		launcher := release.Loader.Kind + "-server-launch.jar"
		if err = requireInstalledFile(dest, launcher); err != nil {
			return "", err
		}
		return loaderShellQuote(java) + " -jar " + launcher + " nogui", nil
	}
	return installedForgeCommand(dest, java)
}

// Forge 1.17+ moves vanilla into libraries. Read the installed profile rather
// than downloading a second, unused root jar or guessing version-specific paths.
func forgeVanillaPath(installer, dest, game string) (string, error) {
	archive, err := zip.OpenReader(installer)
	if err != nil {
		return "", fmt.Errorf("open installer: %w", err)
	}
	defer archive.Close()
	file, err := archive.Open("install_profile.json")
	if err != nil {
		return "", fmt.Errorf("installer profile: %w", err)
	}
	defer file.Close()
	var profile struct {
		Minecraft     string `json:"minecraft"`
		ServerJarPath string `json:"serverJarPath"`
	}
	if err := json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&profile); err != nil {
		return "", fmt.Errorf("installer profile: %w", err)
	}
	if profile.Minecraft != "" && profile.Minecraft != game {
		return "", fmt.Errorf("installer targets Minecraft %s, not %s", profile.Minecraft, game)
	}
	if profile.ServerJarPath == "" {
		profile.ServerJarPath = "{ROOT}/minecraft_server.{MINECRAFT_VERSION}.jar"
	}
	path := strings.NewReplacer("{ROOT}", dest, "{LIBRARY_DIR}", filepath.Join(dest, "libraries"), "{MINECRAFT_VERSION}", game).Replace(profile.ServerJarPath)
	if strings.ContainsAny(path, "{}") {
		return "", fmt.Errorf("unrecognized installer server path tokens")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dest, path)
	}
	path = filepath.Clean(path)
	relative, err := filepath.Rel(dest, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("installer server path escapes staging directory")
	}
	return path, nil
}

func requireInstalledFile(dest, relative string) error {
	info, err := os.Lstat(filepath.Join(dest, relative))
	if err != nil {
		return fmt.Errorf("installer did not produce %s: %w", relative, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("installer produced an invalid %s", relative)
	}
	return nil
}

func installedForgeCommand(dest, java string) (string, error) {
	argsFile := "unix_args.txt"
	if runtime.GOOS == "windows" {
		argsFile = "win_args.txt"
	}
	var argumentPaths []string
	err := filepath.WalkDir(filepath.Join(dest, "libraries"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Name() != argsFile || entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(dest, path)
		if err != nil {
			return err
		}
		if err := requireInstalledFile(dest, relative); err != nil {
			return err
		}
		argumentPaths = append(argumentPaths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if len(argumentPaths) > 1 {
		return "", fmt.Errorf("installer produced multiple server argument files")
	}
	if len(argumentPaths) == 1 {
		if err := requireInstalledFile(dest, "user_jvm_args.txt"); err != nil {
			return "", err
		}
		return loaderShellQuote(java) + " @user_jvm_args.txt " + loaderShellQuote("@"+argumentPaths[0]) + " nogui", nil
	}
	// Pre-1.17 Forge produces a root executable jar rather than argument files.
	entries, err := os.ReadDir(dest)
	if err != nil {
		return "", err
	}
	launcher := ""
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (!strings.HasPrefix(name, "forge-") && !strings.HasPrefix(name, "minecraftforge-universal-")) || !strings.HasSuffix(name, ".jar") || strings.HasSuffix(name, "-installer.jar") {
			continue
		}
		if err := requireInstalledFile(dest, name); err != nil {
			return "", err
		}
		if launcher != "" {
			return "", fmt.Errorf("installer produced multiple legacy Forge launchers")
		}
		launcher = name
	}
	if launcher == "" {
		return "", fmt.Errorf("installer did not produce a runnable server launcher")
	}
	return loaderShellQuote(java) + " -jar " + loaderShellQuote(launcher) + " nogui", nil
}

func loaderShellQuote(value string) string {
	if !strings.ContainsAny(value, " \t\n\r\"'`$;&|<>()\\") {
		return value
	}
	if runtime.GOOS == "windows" {
		return strconv.Quote(value)
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
