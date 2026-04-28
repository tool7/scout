package main

import (
	"os"

	"github.com/readcube/readcube-scout/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
