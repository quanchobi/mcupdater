# mcupdater

A modded Minecraft server updater, written in Go.
Currently supports Fabric, Forge, NeoForge, and Quilt.
Support for all loaders on Modrinth is planned, but not yet implemented.
Mods that are not on Modrinth are currently not supported.

> [!WARNING]
> This does not back up your world folder. Please create manual world backups before use.

## Install

Requires Go 1.22 or later.

```
go install github.com/quanchobi/mcupdater@latest
```

This places the `mcupdater` binary in your `$GOPATH/bin` (default `~/go/bin`). Ensure this directory is in your `PATH`.

Verify the installation:

```
mcupdater --help
```

## Run

Run `mcupdater` from your Minecraft server directory. The updater reads the config file, creates a backup of your current mods, then downloads updated versions.

```
cd /path/to/your/server
mcupdater update
```

The updater checks for a config file in this order:
1. `./config.toml` (current directory)
2. `~/.config/mcupdater.toml` (user config directory)
3. Path specified with `-c` or `--config` flag

## Use

### Config file

The only required field is the Minecraft version. For the loader and mod versions, if the `version` field is omitted, the latest version matching your Minecraft version is chosen.

Mods essential for server function go in `mods.required` — the updater stops if any are unavailable.
Non-essential mods go in `mods.optional` — the updater warns but continues if unavailable.

Example:

```toml
[config]
backup_path = "/var/lib/mcupdater/backup"
log_path = "/var/log/mcupdater.log"
mods_path = "./mods"

[minecraft]
version = "1.21.1"

[loader]
name = "fabric"
# version omitted = latest

[[mods.required]]
name = "fabric-api"

[[mods.required]]
name = "dungeons-and-taverns"
version = "v4.4.4+mod"

[[mods.optional]]
name = "lithium"

[[mods.optional]]
name = "noisium"

[[mods.optional]]
name = "netherportalfix"
version = "21.11.2+fabric-1.21.11"
```

### Commands

| Command | Description |
|---------|-------------|
| `mcupdater update` | Backup current mods, then download updates |
| `mcupdater update --dry-run` | Show what would change without downloading |
| `mcupdater rollback` | Restore from the most recent backup |
| `mcupdater rollback <datetime>` | Restore from a specific backup (e.g. `2025-01-15_143022`) |

### Flags

| Flag | Description |
|------|-------------|
| `-c, --config` | Path to config file |
| `--dry-run` | Show changes without applying |
| `-h, --help` | Show help |

### Recipes

**Preview changes before updating:**

```
mcupdater update --dry-run
```

Shows which mods would be added or updated. No files are downloaded.

**Rollback to last successful update:**

```
mcupdater rollback
```

Restores the most recent backup. Creates a backup of current state first.

**Rollback to a specific backup:**

```
mcupdater rollback 2025-01-15_143022
```

Matches the datetime portion of the backup filename.

**Use a config from a custom location:**

```
mcupdater update -c /etc/mcupdater/prod.toml
```

### Systemd timer

For automatic updates, set up a systemd timer:

`/etc/systemd/system/mcupdater.service`:
```ini
[Unit]
Description=Minecraft Server Updater

[Service]
Type=oneshot
ExecStart=/home/youruser/go/bin/mcupdater update
WorkingDirectory=/path/to/server
```

`/etc/systemd/system/mcupdater.timer`:
```ini
[Unit]
Description=Run mcupdater daily

[Timer]
OnCalendar=daily
Persistent=true

[Install]
WantedBy=timers.target
```

Enable with:
```
systemctl enable --now mcupdater.timer
```
