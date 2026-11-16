// Command herdr-share shares a running Herdr terminal session through a relay.
package main

import (
	"os"

	"github.com/jtsang4/herdr-share/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
