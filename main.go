package main

import (
	"os"

	"github.com/fabhiantomaoludyo/gws-tui/cmd"
)

func main() {
	os.Exit(cmd.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
