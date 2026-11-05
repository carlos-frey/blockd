// blockctl is the command-line client of blockd; see `blockctl --help`.
package main

import (
	"os"

	"github.com/carlos-frey/blockd/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
