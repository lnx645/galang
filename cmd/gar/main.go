// Command gar is the Garurda interpreter.
package main

import (
	"os"

	"garurda/internal/delivery/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
