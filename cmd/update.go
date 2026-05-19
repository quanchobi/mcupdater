package cmd

import (
	"fmt"
	"io"

	"github.com/quanchobi/mcupdater/internal/api"
	"github.com/quanchobi/mcupdater/internal/config"
	"github.com/quanchobi/mcupdater/internal/logger"
)

func resolveChanges(cfg config.Config) ([]api.ModChange, error) {
	return api.ResolveModChanges(cfg, log)
}

func printChanges(w io.Writer, changes []api.ModChange) {
	for _, c := range changes {
		if c.IsNew {
			fmt.Fprintf(w, "  [NEW]     %s -> %s\n", c.Name, c.NewVersion)
		} else if c.IsUpdate {
			fmt.Fprintf(w, "  [UPDATE]  %s: %s -> %s\n", c.Name, c.OldVersion, c.NewVersion)
		}
	}
	fmt.Fprintln(w)
}

func applyChanges(cfg config.Config, log *logger.Logger) error {
	_, err := api.ResolveModChanges(cfg, log)
	return err
}
