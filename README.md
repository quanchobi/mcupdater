# mcupdater

A command-line updater for Minecraft servers. It updates Minecraft, the mod loader, and configured mods together, showing a plan before changing server files.

Supported server types: **Vanilla, Fabric, Quilt, Forge, and NeoForge**. Mods are resolved from **Modrinth** and **CurseForge** using their published Minecraft-version, loader, and dependency metadata.

> Stop the server and take a full world backup before updating. mcupdater backs up replaced files, not worlds. It does not manage the server process, accept the Minecraft EULA, or guarantee that a mod combination will run correctly.

## Requirements

- **Go 1.24 or newer**.
- **Java compatible with your target Minecraft version and loader**. The update plan reports Minecraft's required Java major version. Modded installations run official Java-based installers; installing Vanilla itself does not require Java, but running the server does.
- Internet access to Minecraft, loader, and mod distribution services.
- Write access and enough disk space for staged downloads and backups in the server directory.
- A server directory without symlinks in its path or managed file paths.

## Build

```sh
git clone https://github.com/quanchobi/mcupdater.git
cd mcupdater
go build -o mcupdater .
./mcupdater help
```

On Windows, build with `go build -o mcupdater.exe .` and invoke `.\mcupdater.exe`. The examples below use a POSIX shell; the command flags are the same on Windows.

## Set up an existing server

Choose the server type and target Minecraft version explicitly; `init` identifies mods, not the server's installed Minecraft or loader version.

```sh
./mcupdater init -config mcupdater.json -server-dir /path/to/server -loader fabric -minecraft 1.21.1
```

Replace the path, loader, and version with your own. Supported `-loader` values are `vanilla`, `fabric`, `quilt`, `forge`, and `neoforge`.

`init`:

- Requires an existing server directory and refuses to overwrite an existing config.
- Identifies every `mods/*.jar` by content, rather than guessing from filenames.
- Refuses unknown or ambiguous JARs and duplicate copies of the same project.
- Records discovered mods as mandatory, with their existing filenames.
- Writes configuration only; it does not install or update the server.

Review the generated config before updating. Add any required dependencies and mark genuinely optional mods with `"mandatory": false`.

The `-server-dir` argument is relative to the working directory. `init` records its absolute path. Defaults are `-config mcupdater.json`, `-server-dir .`, `-loader fabric`, `-minecraft latest`, `-loader-version latest`, and `-java java`.

### Start with a new server directory

Instead of discovering an existing installation, copy the supplied [example configuration](mcupdater.example.json). It targets Minecraft 1.21.1 with Fabric and Fabric API:

```sh
mkdir -p server
cp mcupdater.example.json mcupdater.json
```

Edit that configuration before continuing. Use this approach only when `mcupdater.json` does not already contain configuration you want to keep.

### CurseForge access

Set your CurseForge API key before using CurseForge projects or discovering CurseForge JARs:

```sh
export CURSEFORGE_API_KEY='your-api-key'
```

In PowerShell:

```powershell
$env:CURSEFORGE_API_KEY = 'your-api-key'
```

Keep the key out of configuration files and version control. Modrinth does not require this key.

## Check and update

Preview the target versions and any removals without writing files:

```sh
./mcupdater check -config mcupdater.json
```

After reviewing the plan, stopping the server, and backing up its world, apply it:

```sh
./mcupdater update -config mcupdater.json
```

When changes are needed, `update` asks `Proceed with update? [Y|n]`. **Pressing Enter accepts**; enter `n` to cancel. End-of-input without a response cancels. There is no auto-confirm flag. If nothing needs changing, the command reports `Already up to date.` without prompting.

An accepted update downloads and installs into a staging directory, then replaces the managed files and saves a backup. On success, it prints the backup location and a command to launch the server from its directory. Review that command and your JVM settings, handle the EULA yourself if required, and start the server manually.

## Configuration

Configuration is JSON. Unknown fields are rejected. Relative `server_dir` paths and relative Java executable paths are resolved from the **configuration file's directory**, not your current working directory. A bare Java command such as `java` is resolved through `PATH`.

| Field | Meaning |
| --- | --- |
| `server_dir` | Server directory; defaults to the config file's directory. |
| `minecraft` | Exact Minecraft version or `"latest"`; defaults to `"latest"`. |
| `loader.kind` | Required: `vanilla`, `fabric`, `quilt`, `forge`, or `neoforge`. |
| `loader.version` | Exact loader version or `"latest"`; defaults to `"latest"` for modded servers. For Vanilla, omit it or use `"latest"`. |
| `java` | Java executable name or path; defaults to `"java"`. |
| `allow_prerelease` | Allow prerelease selection; defaults to `false`. See version selection below. |
| `mods` | Mod project entries. Vanilla requires this to be empty or omitted. |

Each entry in `mods` supports:

| Field | Meaning |
| --- | --- |
| `name` | Optional display name. |
| `platform` | `"modrinth"` or `"curseforge"`. |
| `project_id` | Modrinth project ID/slug or a CurseForge numeric project ID **as a JSON string**. |
| `file` | Existing JAR filename inside `mods/`, such as `fabric-api-installed.jar`; not a path. Omit when adding a new project. |
| `mandatory` | Defaults to `true`. An unavailable mandatory mod blocks the update; an unavailable optional mod is omitted and its installed JAR is removed and backed up. |

For a Vanilla server, a minimal configuration is:

```json
{
  "server_dir": "./server",
  "minecraft": "1.21.1",
  "loader": { "kind": "vanilla" },
  "mods": []
}
```

Vanilla installs `server.jar` directly. Its version is selected by `minecraft`, not `loader.version`.

### Version selection and mod compatibility

- `"latest"` normally selects stable releases. Forge uses its **recommended** promotion.
- `allow_prerelease: true` permits beta/alpha mods and prerelease loader selection; Minecraft `"latest"` follows its snapshot channel, and Forge uses its latest promotion.
- Exact Minecraft and loader pins are honored regardless of prerelease policy. Pin Minecraft when you do not want to move automatically to a new game release.
- Minecraft is resolved first. mcupdater does not silently choose an older Minecraft version to find a supported loader or compatible mods.
- Mods are selected for the target Minecraft version and loader kind. Add required dependencies explicitly; mcupdater does not add them to your config automatically. Missing dependencies block mandatory mods and can cause optional mods to be omitted. Declared conflicts between selected mods block the plan.
- Network, authentication, and malformed-response errors stop the operation, even for optional mods; they are not treated as permission to remove a mod.
- Metadata checks do not prove runtime compatibility or exact loader-version compatibility. Test the updated server before returning it to service.

### Adding and removing mods

To add a project, add an entry to `mods`. If its JAR already exists in the server's `mods/` directory, include its current basename in `file`. Untracked JARs block planning rather than being silently ignored.

After an update, `.mcupdater/state.json` tracks installed versions and filenames. Leave this file under mcupdater's control; you do not need to update `file` whenever a managed mod's filename changes.

Removing a previously managed project from the configuration schedules its JAR for removal on the next accepted update. Making a mod optional can also result in removal when no compatible release or required dependency is available. Removing mods can remove world content: review the plan and retain your world backup.

## Backups and recovery

Updater data lives inside the server directory:

- `.mcupdater/state.json`: managed file hashes, installed versions, and launch command.
- `.mcupdater/lock`: prevents simultaneous updater processes; it does **not** detect or stop a running Minecraft server.
- `.mcupdater/backups/<timestamp>-<suffix>/manifest.json`: paths involved in an update and whether each existed beforehand.
- `.mcupdater/backups/<timestamp>-<suffix>/old/`: previous files, preserving their paths relative to the server directory.

Worlds and other unmanaged data are not backed up. Existing `user_jvm_args.txt` settings are preserved. Managed files may be replaced, so keep your own customizations and full server backup separately.

Ordinary commit failures trigger rollback. Power loss, forced termination, or a reported incomplete rollback may require manual recovery:

1. Stop the server and confirm no updater process is running.
2. Locate the affected update's backup and inspect `manifest.json`.
3. Remove server paths marked `"existed": false`, if present; these did not exist before the update.
4. Restore files present in `old/` to the corresponding server paths, including `.mcupdater/state.json` when backed up. After an interrupted commit, some original files may never have moved into `old/`; leave those originals in place.
5. Restore a separate world backup if needed. Recheck the installation before starting the server.

Remove a stale `.mcupdater/lock` only after confirming that no updater is still running. There is no automatic restore command.

## Troubleshooting

| Problem | What to check |
| --- | --- |
| `config already exists` | Edit the existing config, or use a different `-config` path for a fresh inventory. |
| Unknown or untracked mod JAR | Verify the project identity and configure its `platform`, `project_id`, and `file`. A manually configured known project can be used when content discovery cannot identify the local JAR. |
| Mandatory mod unavailable | Check the target Minecraft version, loader, release availability, and required dependencies. Keep it mandatory unless running without it is acceptable. |
| No compatible loader for the target | Choose an explicitly supported Minecraft/loader pair instead of relying on `"latest"`. |
| CurseForge access failure | Check `CURSEFORGE_API_KEY` and whether the project permits downloads. |
| Java installer failure | Check the configured Java executable and the Minecraft/loader Java requirements. |
| Cannot acquire updater lock | Check for another updater process before treating the lock as stale. |

Run `./mcupdater help` for the command overview or `./mcupdater init -h` for initialization flags.
