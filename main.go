package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"mcupdater/internal/updater"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client := updater.NewClient(os.Getenv("CURSEFORGE_API_KEY"))
	if err := updater.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, client); err != nil {
		fmt.Fprintln(os.Stderr, "mcupdater:", err)
		os.Exit(1)
	}
}
