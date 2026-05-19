package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/quanchobi/mcupdater/internal/backup"
	"github.com/quanchobi/mcupdater/internal/config"
	"github.com/quanchobi/mcupdater/internal/logger"
	"github.com/spf13/cobra"
)

var (
	configPath string
	dryRun     bool
	log        *logger.Logger
)

var rootCmd = &cobra.Command{
	Use:   "mcupdater",
	Short: "A modded Minecraft server updater",
	Long: `mcupdater automatically updates modded Minecraft servers.
Reads configuration from ./config.toml or ~/.config/mcupdater.toml.
Supports Fabric, Forge, NeoForge, and Quilt loaders.`,
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&configPath, "config", "c", "", "path to config file")
	rootCmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "show changes without applying them")
}

func loadConfig() (config.Config, error) {
	path, err := config.FindConfig(configPath)
	if err != nil {
		return config.Config{}, fmt.Errorf("config file not found: %w", err)
	}

	cfg, err := config.ReadConfig(path)
	if err != nil {
		return config.Config{}, fmt.Errorf("failed to read config: %w", err)
	}

	return cfg, nil
}

func initLogger(cfg config.Config) error {
	var err error
	log, err = logger.New(cfg.Config.LogPath)
	if err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	return nil
}

func confirmPrompt(in io.Reader) bool {
	var response string
	fmt.Fprint(os.Stdout, "Proceed with update? [y/N]: ")
	fmt.Fscanln(in, &response)
	return response == "y" || response == "Y"
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update mods and loader",
	Long: `Creates a backup, then updates mods according to the config.
If an error occurs, restores mods from the backup.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		if err := initLogger(cfg); err != nil {
			return err
		}
		defer log.Close()

		log.Info("starting update")

		if dryRun {
			log.Info("dry-run mode enabled")
		}

		changes, err := resolveChanges(cfg)
		if err != nil {
			return err
		}

		if len(changes) == 0 {
			log.Info("no changes needed")
			return nil
		}

		printChanges(os.Stdout, changes)

		if dryRun {
			log.Info("dry-run complete")
			return nil
		}

		if !confirmPrompt(os.Stdin) {
			log.Info("update cancelled")
			return nil
		}

		backupPath, err := backup.CreateBackup(cfg.Config.ModsPath, cfg.Config.BackupPath)
		if err != nil {
			return fmt.Errorf("failed to create backup: %w", err)
		}
		log.Info("backup created: %s", backupPath)

		err = applyChanges(cfg, log)
		if err != nil {
			log.Error("update failed: %v", err)
			log.Info("restoring from backup")
			restoreErr := backup.RestoreBackup(cfg.Config.BackupPath, cfg.Config.ModsPath, "")
			if restoreErr != nil {
				log.Error("restore failed: %v", restoreErr)
			}
			return err
		}

		log.Info("update completed successfully")
		return nil
	},
}

var rollbackCmd = &cobra.Command{
	Use:   "rollback [datetime]",
	Short: "Restore from a backup",
	Long: `Restores mods from a backup.
If no datetime is specified, restores the most recent backup.
Datetime format: YYYY-MM-DD_HHMMSS`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}

		if err := initLogger(cfg); err != nil {
			return err
		}
		defer log.Close()

		datetime := ""
		if len(args) > 0 {
			datetime = args[0]
		}

		log.Info("starting rollback")

		if dryRun {
			log.Info("dry-run mode enabled")
		}

		backups, err := backup.ListBackups(cfg.Config.BackupPath)
		if err != nil {
			return err
		}
		if len(backups) == 0 {
			return fmt.Errorf("no backups found")
		}

		targetBackup := backups[0]
		if datetime != "" {
			found := false
			for _, name := range backups {
				if contains(name, datetime) {
					targetBackup = name
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("no backup matching %q found", datetime)
			}
		}

		log.Info("restoring from %s", targetBackup)

		if dryRun {
			log.Info("dry-run complete")
			return nil
		}

		backupPath, err := backup.CreateBackup(cfg.Config.ModsPath, cfg.Config.BackupPath)
		if err != nil {
			return fmt.Errorf("failed to create pre-rollback backup: %w", err)
		}
		log.Info("pre-rollback backup created: %s", backupPath)

		err = backup.RestoreBackup(cfg.Config.BackupPath, cfg.Config.ModsPath, datetime)
		if err != nil {
			return fmt.Errorf("rollback failed: %w", err)
		}

		log.Info("rollback completed successfully")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(rollbackCmd)
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
