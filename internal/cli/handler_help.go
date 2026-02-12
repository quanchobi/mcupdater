package cli

import "fmt"

func handleHelp(command) error {
	fmt.Print(`
mcupdater automatically updates modded Minecraft servers.
Reads a list of mods and other config data from

Usage:

	mcupdater [command] [args ...]

The commands are:

	update
		Creates a backup, then updates the mods according to the parameters config.json.
		If an error occurs, it restores the original mods and loader from the backup.
		Upon successful updating, the backup is stored at backupPath.
		By default, this is /var/lib/mcupdater/backup.
		A config file can be specified with the --config/-c flag.
		
	backup
		Creates a backup of your current mods at the directory backupPath.
		The backup is zipped to save space, then renamed to its own hash.
		By default, this is /var/lib/mcupdater/backup.

	restore [hash]
		Restores the backup at the specified hash.
		If no hash is specified, the most recent one is used.
		Before restoring, it backs up the current mods.

	help
		Prints this message
`)
	return nil
}
