package api

import (
	"net/http"
	"testing"

	"github.com/quanchobi/mcupdater/internal/config"
)

func TestGetFabricLoader_InvalidVersion(t *testing.T) {
	cfg := config.Config{}
	cfg.Minecraft.Version = "invalid"
	cfg.Loader.Name = "fabric"

	err := getFabricLoader(http.DefaultClient, cfg)
	if err == nil {
		t.Fatal("expected error for invalid version, got nil")
	}
}

func TestGetFabricLoader_IncompatibleVersion(t *testing.T) {
	cfg := config.Config{}
	cfg.Minecraft.Version = "1.13"
	cfg.Loader.Name = "fabric"

	err := getFabricLoader(http.DefaultClient, cfg)
	if err == nil {
		t.Fatal("expected error for incompatible version, got nil")
	}
}

func TestGetNeoForgeLoader_IncompatibleVersion(t *testing.T) {
	cfg := config.Config{}
	cfg.Minecraft.Version = "1.20.1"
	cfg.Loader.Name = "neoforge"

	err := getNeoForgeLoader(http.DefaultClient, cfg)
	if err == nil {
		t.Fatal("expected error for incompatible version, got nil")
	}
}

func TestGetNeoForgeLoader_InvalidVersion(t *testing.T) {
	cfg := config.Config{}
	cfg.Minecraft.Version = "invalid"
	cfg.Loader.Name = "neoforge"

	err := getNeoForgeLoader(http.DefaultClient, cfg)
	if err == nil {
		t.Fatal("expected error for invalid version, got nil")
	}
}
