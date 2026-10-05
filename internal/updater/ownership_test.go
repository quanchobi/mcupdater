package updater

import "testing"

func TestReplaceUnmanagedValidation(t *testing.T) {
	base := Config{Minecraft: "1.21.1", Loader: LoaderConfig{Kind: "fabric", Version: "latest"}}
	for _, ok := range [][]string{{"server.jar"}, {"libraries/"}, {"run.sh", "libraries/net/x.jar"}} {
		cfg := base
		cfg.ReplaceUnmanaged = ok
		if err := ValidateConfig(cfg); err != nil {
			t.Errorf("%v rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/", "/etc/passwd", "../x.jar", "a//b", `a\b`, ".mcupdater/state.json",
		"mods/x.jar", "mods/", "world/", "config/", "eula.txt", "server.properties"} {
		cfg := base
		cfg.ReplaceUnmanaged = []string{bad}
		if err := ValidateConfig(cfg); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCoveredByReplace(t *testing.T) {
	list := []string{"server.jar", "libraries/"}
	for path, want := range map[string]bool{"server.jar": true, "libraries/a/b.jar": true,
		"librariesX/a.jar": false, "server.jar.bak": false, "run.sh": false} {
		if got := coveredByReplace(list, path); got != want {
			t.Errorf("%s: got %v want %v", path, got, want)
		}
	}
}
