# mcupdater

A modded Minecraft server updater, written in Go. 
Currently supports Fabric, Forge, and NeoForge. 
Support for all loaders on modrinth is planned, but not yet implemented.
Mods that are not on Modrinth are currently not supported.

> [!WARNING]
> This does not back up your world folder. Please create manual world backups before use.

## How to use 

1. Install go

2. Install updater

3. Set up config file

The only version that must be specified is the Minecraft version. 
For the loader and mod versions, if the `version` field is `null`, the latest version that matches your minecraft version is chosen.

Mods that are essential for server function should go into the `mods/required` field, and the updater will stop if any of them are not available.
Mods that are not essential go into the `mods/optional` field. The updater will continue, but will warn you that the mod is unavailable.

As an example:
```json
{
    "config": {
        "backupPath": "/var/lib/mcupdater/backup",
        "logPath": "/var/log/mcupdater.log",
        "modsPath": "./mods"
    },
    "minecraft": {
        "version": "1.21.10"
    },
    "loader": {
        "name": "fabric",
        "version": "1.1.1"
    },
    "mods": {
        "required":[
            {"name": "dungeons-and-taverns", "version": null},
            {"name": "terralith", "version": "2.5.13"}
        ],
        "optional":[
            {"name": "lithium", "version": null},
            {"name": "noisium", "version": null},
            {"name": "netherportalfix", "version": "21.11.2+fabric-1.21.11"}
            {"name": "distanthorizons", "version": "2.45-b-1.21.10"}
        ]
    }
}
```

4. Run updater

The updater will automatically check for `./config.json` and then `~/.config/mcupdater.json`. 
If the config file is somewhere else, its location can be specified with the `-c` or `--config` flag.

Backups will be stored at `backupPath`. By default, this is `/var/lib/mcupdater/backup`

### Recipes

1. Rollback

To last successful backup

`mcupdater rollback`

To a specific backup

`mcupdater rollback --hash {hash}`

