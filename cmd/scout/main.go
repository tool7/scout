package main

import (
	"os"

	"scout/internal/cli"
)

var version = "dev"

func main() {
	os.Exit(cli.Execute(version))
}
