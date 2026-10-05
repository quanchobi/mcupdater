package updater

import (
	"errors"
	"net/http"
)

// ErrUnavailable means the platform has no compatible, downloadable release.
// Authentication, transport, and malformed response errors must not wrap it.
var ErrUnavailable = errors.New("no compatible release available")

type Config struct {
	ServerDir       string       `json:"server_dir"`
	Minecraft       string       `json:"minecraft"`
	Loader          LoaderConfig `json:"loader"`
	Java            string       `json:"java,omitempty"`
	AllowPrerelease bool         `json:"allow_prerelease,omitempty"`
	Mods            []Mod        `json:"mods"`
	// ReplaceUnmanaged acknowledges existing files mcupdater did not install but
	// may replace (moving them to the backup). A trailing "/" covers a directory.
	ReplaceUnmanaged []string `json:"replace_unmanaged,omitempty"`
}

type LoaderConfig struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
	// Fabric only. "installer" (default): vanilla server.jar plus
	// fabric-server-launch.jar and libraries/. "launcher": one self-bootstrapping
	// Fabric launcher at LauncherFile that downloads its own files into .fabric/.
	Layout       string `json:"layout,omitempty"`
	LauncherFile string `json:"launcher_file,omitempty"`
}

type Mod struct {
	Name      string `json:"name,omitempty"`
	Platform  string `json:"platform"`
	ProjectID string `json:"project_id"`
	File      string `json:"file,omitempty"`
	Mandatory *bool  `json:"mandatory,omitempty"`
}

func (m Mod) Required() bool { return m.Mandatory == nil || *m.Mandatory }
func (m Mod) Key() string    { return m.Platform + ":" + m.ProjectID }
func (m Mod) Label() string {
	if m.Name != "" {
		return m.Name + " (" + m.Key() + ")"
	}
	return m.Key()
}

type Artifact struct {
	URL           string
	Filename      string
	HashAlgorithm string
	Hash          string
	Size          int64
}

// Dependency refers to another project/version on the release's own platform.
type Dependency struct {
	ProjectID string
	VersionID string
	Kind      string // required or incompatible; optional/embedded dependencies need no action
}

type ModRelease struct {
	ProjectID    string
	VersionID    string
	Version      string
	Artifact     Artifact
	Dependencies []Dependency
}

type ServerRelease struct {
	Minecraft string
	Loader    LoaderConfig
	Vanilla   Artifact
	Installer Artifact
	JavaMajor int
}

// Client uses official service URLs; HTTP may be supplied for isolated testing.
type Client struct {
	HTTP          *http.Client
	CurseForgeKey string
}
