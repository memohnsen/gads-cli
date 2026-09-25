// Command googleads is the CLI entry point: it hands the process arguments
// to the cli package and turns its result into an exit code.
package main

import (
	"os"

	"github.com/wir-drei-digital/google-ads-cli/internal/cli"
)

func main() { os.Exit(cli.Execute(os.Args[1:])) }
