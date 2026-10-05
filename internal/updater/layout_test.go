package updater

import "testing"

func TestNormalizeLoader(t *testing.T) {
	cases := []struct{ in, want LoaderConfig }{
		{LoaderConfig{Kind: "fabric", Version: "1"}, LoaderConfig{Kind: "fabric", Version: "1", Layout: "installer"}},
		{LoaderConfig{Kind: "fabric", Layout: "launcher"}, LoaderConfig{Kind: "fabric", Layout: "launcher", LauncherFile: "server.jar"}},
		{LoaderConfig{Kind: "fabric", Layout: "launcher", LauncherFile: "fabric.jar"}, LoaderConfig{Kind: "fabric", Layout: "launcher", LauncherFile: "fabric.jar"}},
		{LoaderConfig{Kind: "quilt", Version: "1"}, LoaderConfig{Kind: "quilt", Version: "1"}},
		{LoaderConfig{}, LoaderConfig{}},
	}
	for _, c := range cases {
		if got := normalizeLoader(c.in); got != c.want {
			t.Errorf("%+v: got %+v want %+v", c.in, got, c.want)
		}
	}
}

func TestLayoutValidation(t *testing.T) {
	valid := []LoaderConfig{
		{Kind: "fabric", Version: "latest"},
		{Kind: "fabric", Version: "latest", Layout: "installer"},
		{Kind: "fabric", Version: "latest", Layout: "launcher"},
		{Kind: "fabric", Version: "latest", Layout: "launcher", LauncherFile: "server.jar"},
	}
	invalid := []LoaderConfig{
		{Kind: "quilt", Version: "latest", Layout: "launcher"},
		{Kind: "forge", Version: "latest", LauncherFile: "server.jar"},
		{Kind: "fabric", Version: "latest", Layout: "shim"},
		{Kind: "fabric", Version: "latest", Layout: "installer", LauncherFile: "server.jar"},
		{Kind: "fabric", Version: "latest", Layout: "launcher", LauncherFile: "../server.jar"},
		{Kind: "fabric", Version: "latest", Layout: "launcher", LauncherFile: "server.zip"},
	}
	for _, l := range valid {
		if err := ValidateConfig(Config{Minecraft: "1.21.1", Loader: l}); err != nil {
			t.Errorf("%+v rejected: %v", l, err)
		}
	}
	for _, l := range invalid {
		if err := ValidateConfig(Config{Minecraft: "1.21.1", Loader: l}); err == nil {
			t.Errorf("%+v accepted", l)
		}
	}
}
