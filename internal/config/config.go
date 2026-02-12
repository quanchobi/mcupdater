package config

import (
	"encoding/json"
	"os"
)

type Config struct {
	Minecraft struct {
		Version string `json:"version"`
		Path    string `json:"path"`
	} `json:"minecraft"`
	Loader struct {
		Name    string `json:"name"`
		Version string `json:"version"` // "latest" or version
	} `json:"loader"`
	Mods struct {
		Directory string `json:"path"`
		List      []struct {
			Specifier string `json:"specifier"` // modrinth slug or ID
			Version   string `json:"version"`   // "latest" or version
			Required  string `json:"required"`
		} `json:"list"`
	} `json:"mods"`
}

func readConfig(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}

	defer file.Close()

	decoder := json.NewDecoder(file)
	cfg := Config{}
	err = decoder.Decode(&cfg)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}
