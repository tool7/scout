package main

import (
	"os"

	"scout/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
