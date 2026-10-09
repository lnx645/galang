// Command gar is the GaLang interpreter.
package main

import (
	"os"

	"galang/internal/delivery/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
