package api

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/quanchobi/mcupdater/internal/config"
)

type fabricMeta struct {
	Loader struct {
		Separator string `json:"separator"`
		Build     int    `json:"build"`
		Maven     string `json:"maven"`
		Version   string `json:"version"`
		Stable    bool   `json:"stable"`
	} `json:"loader"`
	Intermediary struct {
		Maven   string `json:"maven"`
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	} `json:"intermediary"`
	LauncherMeta struct {
		Version        int `json:"version"`
		MinJavaVersion int `json:"min_java_version"`
		Libraries      struct {
			Client []any `json:"client"`
			Common []struct {
				Name   string `json:"name"`
				URL    string `json:"url"`
				Md5    string `json:"md5"`
				Sha1   string `json:"sha1"`
				Sha256 string `json:"sha256"`
				Sha512 string `json:"sha512"`
				Size   int    `json:"size"`
			} `json:"common"`
			Server      []any `json:"server"`
			Development []struct {
				Name   string `json:"name"`
				URL    string `json:"url"`
				Md5    string `json:"md5"`
				Sha1   string `json:"sha1"`
				Sha256 string `json:"sha256"`
				Sha512 string `json:"sha512"`
				Size   int    `json:"size"`
			} `json:"development"`
		} `json:"libraries"`
		MainClass struct {
			Client string `json:"client"`
			Server string `json:"server"`
		} `json:"mainClass"`
	} `json:"launcherMeta"`
}

type neoForgeMaven struct {
	XMLName    xml.Name `xml:"metadata"`
	Text       string   `xml:",chardata"`
	GroupId    string   `xml:"groupId"`
	ArtifactId string   `xml:"artifactId"`
	Versioning struct {
		Text     string   `xml:",chardata"`
		Latest   string   `xml:"latest"`
		Release  string   `xml:"release"`
		Versions struct {
			Text    string   `xml:",chardata"`
			Version []string `xml:"version"`
		} `xml:"versions"`
		LastUpdated string `xml:"lastUpdated"`
	} `xml:"versioning"`
}

type forgePromotions struct {
	Homepage string            `json:"homepage"`
	Promos   map[string]string `json:"promos"`
}

type quiltMeta struct {
	Loader struct {
		Separator string `json:"separator"`
		Build     int    `json:"build"`
		Maven     string `json:"maven"`
		Version   string `json:"version"`
	} `json:"loader"`
	Hashed struct {
		Maven   string `json:"maven"`
		Version string `json:"version"`
	} `json:"hashed"`
	Intermediary struct {
		Maven   string `json:"maven"`
		Version string `json:"version"`
	} `json:"intermediary"`
	LauncherMeta struct {
		Version        int `json:"version"`
		MinJavaVersion int `json:"min_java_version"`
		Libraries      struct {
			Client []interface{} `json:"client"`
			Common []struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"common"`
			Server      []interface{} `json:"server"`
			Development []struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"development"`
		} `json:"libraries"`
		MainClass struct {
			Client         string `json:"client"`
			Server         string `json:"server"`
			ServerLauncher string `json:"serverLauncher"`
		} `json:"mainClass"`
	} `json:"launcherMeta"`
}

func validateMinecraftVersion(version string) error {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return fmt.Errorf("invalid minecraft version format: %s", version)
	}
	return nil
}

func isFabricCompatible(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return false
	}
	if parts[0] == "1" {
		return true
	}
	if len(parts[0]) > 0 && unicode.IsLetter(rune(parts[0][0])) {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	return major >= 26
}

func isNeoForgeCompatible(version string) bool {
	parts := strings.Split(version, ".")
	if len(parts) < 3 {
		return false
	}
	if parts[0] != "1" {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	if minor > 20 {
		return true
	}
	if minor == 20 {
		patch, err := strconv.Atoi(parts[2])
		if err != nil {
			return false
		}
		return patch >= 2
	}
	return false
}

func selectNeoForgeVersion(availableVersions []string, targetMajor string) string {
	for i := len(availableVersions) - 1; i >= 0; i-- {
		versionCandidate := availableVersions[i]
		candidateParts := strings.Split(versionCandidate, ".")
		if len(candidateParts) > 0 && candidateParts[0] == targetMajor {
			return versionCandidate
		}
	}
	return ""
}

func forgePromoKey(mcVersion string) string {
	return fmt.Sprintf("%s-recommended", mcVersion)
}

func getFabricLoader(client *http.Client, cfg config.Config) error {
	if err := validateMinecraftVersion(cfg.Minecraft.Version); err != nil {
		return err
	}

	if !isFabricCompatible(cfg.Minecraft.Version) {
		return fmt.Errorf("fabric loader requires minecraft version >= 1.14")
	}

	installerVersion := "1.1.1"

	baseURL := "https://meta.fabricmc.net/v2/versions/loader"

	fabricVersion := cfg.Loader.Version
	if fabricVersion == "" {
		var versionList []fabricMeta

		url := fmt.Sprintf("%s/%s", baseURL, cfg.Minecraft.Version)
		resp, err := client.Get(url)

		if err != nil {
			return err
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("API request at endpoint %s failed with status: %d", url, resp.StatusCode)
		}
		defer resp.Body.Close()

		decoder := json.NewDecoder(resp.Body)
		err = decoder.Decode(&versionList)
		if err != nil {
			return err
		}

		fabricVersion = versionList[0].Loader.Version
	} else {
		fabricVersion = cfg.Loader.Version
	}

	if fabricVersion == "" {
		return fmt.Errorf("no fabric loader version found for minecraft version %s", cfg.Minecraft.Version)
	}

	fabricURL := fmt.Sprintf("%s/%s/%s/%s/server/jar", baseURL, cfg.Minecraft.Version, fabricVersion, installerVersion)
	filePath := fmt.Sprintf("./fabric-server-mcfg.%s-loader.%s-launcher.%s.jar", cfg.Minecraft.Version, fabricVersion, installerVersion)

	err := downloadFile(client, filePath, fabricURL)

	if err != nil {
		return err
	}
	return nil
}

func getNeoForgeLoader(client *http.Client, cfg config.Config) error {
	if err := validateMinecraftVersion(cfg.Minecraft.Version); err != nil {
		return err
	}

	if !isNeoForgeCompatible(cfg.Minecraft.Version) {
		return fmt.Errorf("neoforge requires minecraft version >= 1.20.2")
	}

	mcVersionParts := strings.Split(cfg.Minecraft.Version, ".")
	targetMajorVersion := mcVersionParts[1]

	neoForgeVersion := cfg.Loader.Version
	if neoForgeVersion == "" {
		mavenURL := "https://maven.neoforged.net/releases/net/neoforged/neoforge/maven-metadata.xml"
		var mavenMetadata neoForgeMaven
		resp, err := client.Get(mavenURL)
		if err != nil {
			return err
		}

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("API request at endpoint %s failed with status: %d", mavenURL, resp.StatusCode)
		}

		defer resp.Body.Close()
		decoder := xml.NewDecoder(resp.Body)
		if err := decoder.Decode(&mavenMetadata); err != nil {
			return fmt.Errorf("failed to decode NeoForge maven metadata: %w", err)
		}
		availableVersions := mavenMetadata.Versioning.Versions.Version

		neoForgeVersion = selectNeoForgeVersion(availableVersions, targetMajorVersion)
	} else {
		neoForgeVersion = cfg.Loader.Version
	}

	if neoForgeVersion == "" {
		return fmt.Errorf("no neoforge version found for minecraft version %s", cfg.Minecraft.Version)
	}

	neoForgeURL := fmt.Sprintf("https://maven.neoforged.net/releases/net/neoforged/neoforge/%s/%s-installer.jar", neoForgeVersion, neoForgeVersion)
	filePath := fmt.Sprintf("./neoforge-%s-installer.jar", neoForgeVersion)

	err := downloadFile(client, filePath, neoForgeURL)

	if err != nil {
		return err
	}
	return nil
}

func getForgeLoader(client *http.Client, cfg config.Config) error {
	baseURL := "https://maven.minecraftforge.net/net/minecraftforge/forge"
	forgeVersion := cfg.Loader.Version
	if forgeVersion == "" {
		promotionsSlimURL := "https://files.minecraftforge.net/net/minecraftforge/forge/promotions_slim.json"
		var promos forgePromotions

		resp, err := client.Get(promotionsSlimURL)
		if err != nil {
			return err
		}

		defer resp.Body.Close()

		decoder := json.NewDecoder(resp.Body)
		decoder.Decode(&promos)
		versionKey := forgePromoKey(cfg.Minecraft.Version)
		forgeVersion = promos.Promos[versionKey]
	}

	filePath := fmt.Sprintf("./forge-%s-%s-installer.jar", cfg.Minecraft.Version, forgeVersion)
	forgeURL := fmt.Sprintf("%s/%s-%s/forge-%s-installer.jar", baseURL, cfg.Minecraft.Version, forgeVersion, forgeVersion)

	err := downloadFile(client, filePath, forgeURL)
	if err != nil {
		return err
	}

	return nil
}

func getQuiltLoader(client *http.Client, cfg config.Config) error {
	baseURL := "quiltmc.org/repository/release/org/quiltmc/quilt-loader"

	quiltVersion := cfg.Loader.Version
	if quiltVersion == "" {
		quiltMetaBaseURL := "meta.quiltmc.org/v3/versions/loader"
		metaURL := fmt.Sprintf("%s/%s", quiltMetaBaseURL, cfg.Minecraft.Version)
		req, err := http.NewRequest("GET", metaURL, nil)
		if err != nil {
			return err
		}

		req.Header.Set("User-Agent", "github_quanchobi/mcupdater/0.1 (contact@quanchobi.io)")

		resp, err := client.Do(req)
		if err != nil {
			return err
		}

		defer resp.Body.Close()

		var metadata []quiltMeta

		decoder := json.NewDecoder(resp.Body)
		if err = decoder.Decode(&metadata); err != nil {
			return err
		}
		quiltVersion = metadata[0].Loader.Version
	}

	filepath := fmt.Sprintf("quilt-loader-%s.jar", quiltVersion)
	quiltURL := fmt.Sprintf("%s/%s/quilt-loader-%s.jar", baseURL, quiltVersion, quiltVersion)

	err := downloadFile(client, filepath, quiltURL)
	if err != nil {
		return err
	}

	return nil
}
