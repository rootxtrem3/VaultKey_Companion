package main

import (
	"os"

	"github.com/rootxtrem3/VaultKey_Companion/internal/cli"
)

var (
	version   = "0.1.0"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr,
		version, commit, buildDate))
}
