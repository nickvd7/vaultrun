package main

import (
	"os"

	"github.com/nickvd7/vaultrun/cmd/cli/commands"
)

func main() {
	if err := commands.Root().Execute(); err != nil {
		os.Exit(1)
	}
}
