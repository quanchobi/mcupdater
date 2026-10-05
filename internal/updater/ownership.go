package updater

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// predictedServerFiles lists root files the selected installation writes, where
// that is knowable without running an installer. Forge/NeoForge outputs are only
// known after staging; Apply's ownership check covers them.
func predictedServerFiles(r ServerRelease) []string {
	switch r.Loader.Kind {
	case "vanilla":
		return []string{"server.jar"}
	case "fabric":
		if r.Loader.Layout == "launcher" {
			return []string{r.Loader.LauncherFile}
		}
		return []string{"server.jar", "fabric-server-launch.jar"}
	case "quilt":
		return []string{"server.jar", "quilt-server-launch.jar"}
	}
	return nil
}

// checkOwnership enforces the ownership rule: an existing live file may be
// replaced only if mcupdater installed it (it is in state), it is a verified
// launcher being adopted, or the administrator acknowledged it in
// replace_unmanaged. staged lists the relative paths the update would write.
func checkOwnership(cfg Config, p Plan, staged map[string]string) error {
	var collisions []string
	for rel := range staged {
		if _, managed := p.Previous.Files[rel]; managed || coveredByReplace(cfg.ReplaceUnmanaged, rel) {
			continue
		}
		// mods/ is governed by identity tracking instead: inventory refuses any
		// JAR that is not configured or recorded, so every existing one is known.
		if name, isMod := strings.CutPrefix(rel, "mods/"); isMod {
			if _, tracked := p.Inventory[name]; tracked {
				continue
			}
		}
		if _, adopted := p.Adopted[rel]; adopted {
			continue
		}
		live, err := safePath(cfg.ServerDir, rel)
		if err != nil {
			return err
		}
		if _, err = os.Lstat(live); err == nil {
			collisions = append(collisions, rel)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for rel, adopted := range p.Adopted {
		live, err := safePath(cfg.ServerDir, rel)
		if err != nil {
			return err
		}
		if got, err := fileHash(live); err != nil || got != adopted.SHA256 {
			return fmt.Errorf("%s changed after it was verified for adoption; retry", rel)
		}
	}
	if len(collisions) == 0 {
		return nil
	}
	sort.Strings(collisions)
	return unmanagedError(collisions)
}

func unmanagedError(paths []string) error {
	return fmt.Errorf("refusing to overwrite files mcupdater did not install; no files changed:\n  %s\n"+
		"If they may be replaced (they will be moved to the backup), add them to \"replace_unmanaged\" in the config, "+
		"for example \"replace_unmanaged\": [%q]. A trailing \"/\" covers a directory.",
		strings.Join(paths, "\n  "), paths[0])
}
