package updater

// fabricLauncherInfo is what a Fabric executable server launcher declares: the
// installer it was built from (manifest) and the loader/game it bootstraps
// (install.properties).
type fabricLauncherInfo struct {
	Installer, Loader, Minecraft string
}
