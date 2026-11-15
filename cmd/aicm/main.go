// Command aicm is the CLI entry point.
package main

import (
	"os"

	"github.com/routeflags/ai-citation-monitor/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
