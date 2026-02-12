package api

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
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
		Text     string `xml:",chardata"`
		Latest   string `xml:"latest"`
		Release  string `xml:"release"`
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

// Downloads a version of the Fabric loader for the user.
// This version can be specified by setting the loader version in the user's config.json file.
// Additionally, if no loader version is specified, the latest Fabric version for the Minecraft version will be used.
// Takes the user's configuration as an argument.
// Returns an error, if one occurs.
func getFabricLoader(cfg config.Config) error {
	mcVersionParts := strings.Split(cfg.Minecraft.Version, ".")

	if len(mcVersionParts) < 2 {
		return fmt.Errorf("Invalid minecraft version format: %s", cfg.Minecraft.Version)
	}

	// Should filter out beta versions as well as account for Mojang's new numbering scheme (maybe)
	if mcVersionParts[0] != "1" && (mcVersionParts[0] < "26" || unicode.IsLetter(rune(mcVersionParts[0][0]))) {
		return fmt.Errorf("Fabric loader requires Minecraft version >= 1.14")
	}

	installerVersion := "1.1.1" // this installer version might not work for older versions, I'm not sure.

	baseURL := "https://meta.fabricmc.net/v2/versions/loader"

	fabricVersion := cfg.Loader.Version
	if fabricVersion == "" {
		var versionList []fabricMeta

		url := fmt.Sprintf("%s/%s", baseURL, cfg.Minecraft.Version)
		resp, err := http.Get(url)

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
		return fmt.Errorf("No fabric loader version found for Minecraft version %s", cfg.Minecraft.Version)
	}

	fabricURL := fmt.Sprintf("%s/%s/%s/%s/server/jar", baseURL, cfg.Minecraft.Version, fabricVersion, installerVersion)
	filePath := fmt.Sprintf("%s/fabric-server-mcfg.%s-loader.%s-launcher.%s.jar", cfg.Minecraft.Path, cfg.Minecraft.Version, fabricVersion, installerVersion) // recreating the file name.

	err := downloadFile(filePath, fabricURL) // TODO: "server.jar" should rely on the file name from the api.

	if err != nil {
		return err
	}
	return nil
}

// Downloads a version of the NeoForge installer.
// This version can be specified by setting the loader version in the user's config.json file.
// Additionally, if no loader version is specified,
// the function will grab the recommended version of NeoForge for the Minecraft version being used.
// Takes the user's configuration as an argument.
// Returns an error, if one occurs.
func getNeoForgeLoader(cfg config.Config) error {
	/*
		Currently relies heavily on neoForge version numbers shadowing that of Mojangs.
		Might break horribly when Mojang changes their versioning system.
		See: https://www.minecraft.net/en-us/article/minecraft-new-version-numbering-system
	*/
	mcVersionParts := strings.Split(cfg.Minecraft.Version, ".")

	if len(mcVersionParts) < 2 {
		return fmt.Errorf("Invalid minecraft version format: %s", cfg.Minecraft.Version)
	}

	targetMajorVersion := mcVersionParts[1]

	if targetMajorVersion < "20" || (targetMajorVersion == "20" && (len(mcVersionParts) < 2 || mcVersionParts[2] < "2")) {
		return fmt.Errorf("NeoForge requires Minecraft version >= 1.20.2")
	}

	neoForgeVersion := cfg.Loader.Version
	if neoForgeVersion == "" {
		mavenURL := "https://maven.neoforged.net/releases/net/neoforged/neoforge/maven-metadata.xml"
		var mavenMetadata neoForgeMaven
		resp, err := http.Get(mavenURL)
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

		// iterate backwards, assuming the user wants a newer version.
		for i := len(availableVersions) - 1; i >= 0; i-- {
			versionCandidate := availableVersions[i]
			candidateParts := strings.Split(versionCandidate, ".")

			if len(candidateParts) > 0 && candidateParts[0] == targetMajorVersion {
				neoForgeVersion = versionCandidate
				// Probably should be a check here if it is a beta/alpha version or not. If the user does not want a beta, we should skip it.
				break
			}
		}
	} else {
		neoForgeVersion = cfg.Loader.Version
	}

	if neoForgeVersion == "" {
		return fmt.Errorf("No fabric loader version found for Minecraft version %s", cfg.Minecraft.Version)
	}

	neoForgeURL := fmt.Sprintf("https://maven.neoforged.net/releases/net/neoforged/neoforge/%s/%s-installer.jar", neoForgeVersion, neoForgeVersion)
	filePath := fmt.Sprintf("%s/neoforge-%s-installer.jar", cfg.Minecraft.Path, neoForgeVersion)

	err := downloadFile(filePath, neoForgeURL)

	if err != nil {
		return err
	}
	return nil
}

// Downloads a version of the Forge installer.
// This version can be specified by setting the loader version in the user's config.json file.
// Additionally, if no loader version is specified,
// the function will download the recommended version for the Minecraft version being used.
// Takes the user's configuration as an argument.
// Returns an error, if one occurs.
func getForgeLoader(cfg config.Config) error {
	baseURL := "https://maven.minecraftforge.net/net/minecraftforge/forge"
	forgeVersion := cfg.Loader.Version
	if forgeVersion == "" {
		promotionsSlimURL := "https://files.minecraftforge.net/net/minecraftforge/forge/promotions_slim.json"
		var promos forgePromotions

		resp, err := http.Get(promotionsSlimURL)
		if err != nil {
			return err
		}

		defer resp.Body.Close()

		decoder := json.NewDecoder(resp.Body)
		decoder.Decode(&promos)
		versionKey := fmt.Sprintf("%s-recommended", cfg.Minecraft.Version)
		forgeVersion = promos.Promos[versionKey]
	}

	filePath := fmt.Sprintf("%s/forge-%s-%s-installer.jar", cfg.Minecraft.Path, cfg.Minecraft.Version, forgeVersion)
	forgeURL := fmt.Sprintf("%s/%s-%s/forge-%s-installer.jar", baseURL, cfg.Minecraft.Version, forgeVersion, forgeVersion)

	err := downloadFile(filePath, forgeURL)
	if err != nil {
		return err
	}

	return nil
}

func getQuiltLoader(cfg config.Config) error {
	baseURL := "quiltmc.org/repository/release/org/quiltmc/quilt-loader"

	quiltVersion := cfg.Loader.Version
	if quiltVersion == "" {
		quiltMetaBaseURL := "meta.quiltmc.org/v3/versions/loader"
		metaURL := fmt.Sprintf("%s/%s", quiltMetaBaseURL, cfg.Minecraft.Version)
		req, err := http.NewRequest("GET", metaURL, nil)
		if err != nil {
			return err
		}

		req.Header.Set("User-Agent", "github_quanchobi/mcupdater/0.1 (contact@quanchobi.io)") // TODO: this should be an option in the user's config.

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}

		defer resp.Body.Close()

		var metadata []quiltMeta

		decoder := json.NewDecoder(resp.Body)
		if err = decoder.Decode(&metadata); err != nil {
			return err
		}
		quiltVersion = metadata[0].Loader.Version // I think this will always be 0.30.0-beta
	}

	filepath := fmt.Sprintf("quilt-loader-%s.jar", quiltVersion)
	quiltURL := fmt.Sprintf("%s/%s/quilt-loader-%s.jar", baseURL, quiltVersion, quiltVersion)

	err := downloadFile(filepath, quiltURL)
	if err != nil {
		return err
	}

	return nil
}
