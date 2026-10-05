package updater

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const usage = `mcupdater - update stopped vanilla or modded Minecraft servers

Usage:
  mcupdater init   [-config mcupdater.json] [-server-dir .] [-loader fabric]
                  [-minecraft latest] [-loader-version latest] [-java java]
                  [-layout auto|installer|launcher] [-launcher-file server.jar]
  mcupdater check  [-config mcupdater.json]
  mcupdater update [-config mcupdater.json]

Server types (-loader): vanilla, fabric, quilt, forge, neoforge.
Versions may be exact or "latest". For vanilla, minecraft selects the version;
omit loader.version or use "latest", and leave mods empty. Vanilla installs
server.jar directly; Java is needed to run it, not to install it.
"latest" selects stable releases; Forge uses its recommended promotion.
Set allow_prerelease=true for beta/alpha mods, snapshot games, and latest Forge.
Exact Minecraft/loader pins are honored regardless of prerelease policy.
Config paths are relative to the config file, not the current directory.
init identifies every mods/*.jar by content and refuses unknown files.
Mods are mandatory by default; set "mandatory": false for optional mods.
Set CURSEFORGE_API_KEY for CurseForge projects and fingerprint discovery.
check never writes files. update always requires a [Y|n] confirmation.

Fabric has two layouts, recorded by init as loader.layout: "installer" (vanilla
server.jar + fabric-server-launch.jar + libraries/) or "launcher" (one Fabric
launcher, loader.launcher_file, default server.jar). Launchers are verified
against Fabric's Maven installer. Changing loader.layout converts on update.
Existing files mcupdater did not install are never replaced unless listed in
"replace_unmanaged", e.g. ["server.jar"]; check reports them.

Stop the server and take a full world backup before update. The updater backs
up replaced files, not worlds, and never starts the server or accepts its EULA.
Mod dependencies must be in the config; missing required dependencies block
mandatory mods. Compatibility metadata cannot guarantee runtime compatibility.

Example config (project_id is a Modrinth ID/slug or CurseForge numeric string):
{
  "server_dir": "./server",
  "minecraft": "1.21.1",
  "loader": {"kind": "fabric", "version": "latest"},
  "java": "java",
  "allow_prerelease": false,
  "mods": [
    {"name": "Fabric API", "platform": "modrinth", "project_id": "P7dR8mSH",
     "file": "fabric-api-installed.jar", "mandatory": true}
  ]
}
Use file for existing JARs. Omit it when adding a new project. Following an
update, .mcupdater/state.json tracks current filenames; do not edit that file.
Only use directories without symlink components. A stale .mcupdater/lock after
a crash must be removed manually after checking that no updater is running.
Backups live under .mcupdater/backups/<timestamp>/old/ with a manifest.json
listing replaced/created paths. For manual recovery, stop the server, remove
paths marked existed=false, and restore old/ paths including state.json.
Updates roll back ordinary errors; power loss or SIGKILL needs manual recovery.
`

func Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer, c *Client) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(out, usage)
		return nil
	}
	command := args[0]
	if command != "init" && command != "check" && command != "update" {
		return fmt.Errorf("unknown command %q; run mcupdater help", command)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(errOut)
	configPath := flags.String("config", "mcupdater.json", "JSON configuration path")
	var serverDir, loader, minecraft, loaderVersion, java, layout, launcherFile string
	if command == "init" {
		flags.StringVar(&serverDir, "server-dir", ".", "existing server directory")
		flags.StringVar(&loader, "loader", "fabric", "vanilla, fabric, quilt, forge, or neoforge")
		flags.StringVar(&minecraft, "minecraft", "latest", "Minecraft version or latest")
		flags.StringVar(&loaderVersion, "loader-version", "latest", "loader version or latest (unused for vanilla)")
		flags.StringVar(&java, "java", "java", "Java executable for installers and the launch command")
		flags.StringVar(&layout, "layout", "auto", "Fabric layout: auto, installer, or launcher")
		flags.StringVar(&launcherFile, "launcher-file", "", "Fabric launcher JAR the server starts (implies the launcher layout)")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if command == "init" {
		if _, err := os.Lstat(*configPath); err == nil {
			return fmt.Errorf("config already exists: %s", *configPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		root, err := filepath.Abs(serverDir)
		if err != nil {
			return err
		}
		if _, err = safePath(root, "mods"); err != nil {
			return err
		}
		if info, e := os.Stat(root); e != nil {
			return e
		} else if !info.IsDir() {
			return fmt.Errorf("server-dir is not a directory")
		}
		cfg := Config{ServerDir: root, Minecraft: minecraft, Loader: LoaderConfig{Kind: loader, Version: loaderVersion}, Java: java}
		if err = ValidateConfig(cfg); err != nil {
			return err
		}
		var notes []string
		if loader == "fabric" {
			detected, message, err := detectFabricLayout(root, layout, launcherFile)
			if err != nil {
				return err
			}
			cfg.Loader.Layout, cfg.Loader.LauncherFile = detected.Layout, detected.LauncherFile
			notes = append(notes, message)
			if detected.Layout == "launcher" && minecraft != "latest" {
				if info, err := inspectLauncherFile(filepath.Join(root, detected.LauncherFile)); err == nil && info.Minecraft != minecraft {
					notes = append(notes, fmt.Sprintf("Note: %s currently targets Minecraft %s; the config targets %s.", detected.LauncherFile, info.Minecraft, minecraft))
				}
			}
		} else if layout != "auto" || launcherFile != "" {
			return fmt.Errorf("-layout and -launcher-file apply only to -loader fabric")
		}
		release := ServerRelease{Minecraft: minecraft, Loader: normalizeLoader(cfg.Loader)}
		for _, rel := range predictedServerFiles(release) {
			if rel == cfg.Loader.LauncherFile {
				continue // a launcher here is adopted after verification
			}
			if _, err := os.Lstat(filepath.Join(root, rel)); err == nil {
				notes = append(notes, fmt.Sprintf("Warning: %s exists but was not installed by mcupdater; check and update will refuse to replace it until you add it to \"replace_unmanaged\".", rel))
			}
		}
		cfg.Mods, err = c.Discover(ctx, root)
		if err != nil {
			return err
		}
		if err = ValidateConfig(cfg); err != nil {
			return err
		}
		if err = WriteConfig(*configPath, cfg); err != nil {
			return err
		}
		for _, note := range notes {
			fmt.Fprintln(out, note)
		}
		fmt.Fprintf(out, "Wrote %s with %d mandatory mods. Review versions and mark optional mods with mandatory=false before updating.\n", *configPath, len(cfg.Mods))
		return nil
	}
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		return err
	}
	if command == "update" {
		release, err := Lock(cfg.ServerDir)
		if err != nil {
			return err
		}
		defer release()
	}
	p, err := c.BuildPlan(ctx, cfg)
	if err != nil {
		return err
	}
	p.Print(out)
	if command == "check" || !p.Changed {
		return nil
	}
	fmt.Fprintln(out, "WARNING: Stop the Minecraft server and back up your world first. Updating may remove optional mods and their content. This tool does not manage the server process or back up worlds.")
	fmt.Fprintln(out, "Confirming asserts that the server is stopped. Only replaced server/mod files are backed up.")
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1024), 4096)
	for {
		fmt.Fprint(out, "Proceed with update? [Y|n] ")
		if !scanner.Scan() {
			if err = scanner.Err(); err != nil {
				return err
			}
			fmt.Fprintln(out, "Cancelled (no confirmation).")
			return nil
		}
		switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
		case "n", "no":
			fmt.Fprintln(out, "Cancelled.")
			return nil
		case "", "y", "yes":
			_, err = c.Apply(ctx, cfg, p, out)
			return err
		default:
			fmt.Fprintln(out, "Enter y or n.")
		}
	}
}
