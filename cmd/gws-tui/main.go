package main

import (
	"os"

	"github.com/fabhiansan/gws-tui/cmd"
)

func main() {
	os.Exit(cmd.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
