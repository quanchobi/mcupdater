# ADR 0001: Server file ownership

- Status: accepted
- Date: 2026-10-05

## Context

`update` stages a complete installation and then moves every staged path into
place. Any existing live file at those paths went to the backup. There was no
notion of who owned a path.

A live test on a production Fabric server showed what that costs. The server
ran Fabric's *executable launcher* as `server.jar`: a 179 KB jar with
`Main-Class: net.fabricmc.installer.ServerLauncher` that downloads vanilla into
`.fabric/` at boot. Its systemd unit ran `java -jar server.jar nogui`.
mcupdater only knew the installer layout (vanilla at `server.jar`, plus
`fabric-server-launch.jar`). It replaced the launcher with vanilla Minecraft,
and the plan never mentioned it. A restart would have booted vanilla and
silently dropped every mod.

## Decision

1. **Ownership rule.** An existing live file may be replaced only if:
   - it is recorded in `.mcupdater/state.json` (mcupdater installed it), or
   - it is a Fabric launcher verified against Fabric's Maven installer and is
     being adopted, or
   - the administrator lists it in `replace_unmanaged`.

   Anything else at a path an update would write is a *collision*. `check`
   reports collisions where outputs are predictable (vanilla, Fabric, Quilt).
   `update` checks again after staging, before any live mutation, which also
   covers Forge and NeoForge, whose outputs are known only after their
   installer runs.
2. **Both Fabric layouts are supported** (`installer` and `launcher`). `init`
   detects the layout once and records it in the config. `update` never
   re-detects it.
3. **Launcher integrity.** Fabric meta publishes no checksum for generated
   launchers. A launcher must equal the checksummed Maven
   `fabric-installer-<v>-server.jar`, entry for entry, plus exactly one
   `install.properties` naming the planned loader and game. An existing
   launcher is verified the same way against the installer version it
   declares.
4. **Conversion is allowed and shown.** Changing `loader.layout` or
   `loader.launcher_file` converts the installation. The plan lists the files
   that are removed (moved to the backup) and the files that are written.
5. **`.fabric/` is unmanaged.** It is the launcher's cache, not ours.

## Consequences

- A first run on a server with hand-installed server files fails until the
  administrator acknowledges them. This friction is intentional, and the
  error names the exact config line to add.
- Protected roots (`mods/`, `world/`, `config/`, `eula.txt`,
  `server.properties`, `.mcupdater/`) cannot be acknowledged.
- Launcher verification depends on Fabric's Maven layout. If Fabric changes
  the launcher format, verification fails closed and names the differing
  entry.

## Rejected alternatives

- **Recognising files by name or content as permission to overwrite.** A
  smarter guess is still a guess, and the next unfamiliar layout breaks it
  the same way. Content inspection is used only to *identify* a launcher for
  adoption. Adoption still requires full verification.
- **Refusing to convert layouts.** Safe, but the maintainer preferred
  conversion as long as the plan makes it visible.
- **Trusting HTTPS alone for meta launchers.** Unnecessary, because the
  launcher can be proven against artifacts that do publish checksums.
