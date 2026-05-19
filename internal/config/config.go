package config

import (
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Config struct {
		BackupPath string `toml:"backup_path"`
		LogPath    string `toml:"log_path"`
		ModsPath   string `toml:"mods_path"`
	} `toml:"config"`
	Minecraft struct {
		Version string `toml:"version"`
	} `toml:"minecraft"`
	Loader struct {
		Name    string `toml:"name"`
		Version string `toml:"version"`
	} `toml:"loader"`
	Mods struct {
		Required []ModEntry `toml:"required"`
		Optional []ModEntry `toml:"optional"`
	} `toml:"mods"`
}

type ModEntry struct {
	Name    string `toml:"name"`
	Version string `toml:"version"`
}

func ReadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	err = toml.Unmarshal(data, &cfg)
	if err != nil {
		return Config{}, err
	}

	if cfg.Config.BackupPath == "" {
		cfg.Config.BackupPath = "/var/lib/mcupdater/backup"
	}
	if cfg.Config.LogPath == "" {
		cfg.Config.LogPath = "/var/log/mcupdater.log"
	}
	if cfg.Config.ModsPath == "" {
		cfg.Config.ModsPath = "./mods"
	}

	return cfg, nil
}

func FindConfig(explicitPath string) (string, error) {
	if explicitPath != "" {
		_, err := os.Stat(explicitPath)
		if err == nil {
			return explicitPath, nil
		}
		return "", err
	}

	candidates := []string{
		"./config.toml",
	}

	homeDir, err := os.UserHomeDir()
	if err == nil {
		candidates = append(candidates, filepath.Join(homeDir, ".config", "mcupdater.toml"))
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", os.ErrNotExist
}
