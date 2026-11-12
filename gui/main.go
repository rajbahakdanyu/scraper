package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/rajbahakdanyu/scraper/gui/backend"
)

//go:embed all:frontend/dist build/appicon.png
var assets embed.FS

func main() {
	if err := backend.Run(assets); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
