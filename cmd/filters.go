package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func runDryRunWithDiskCheck(scraper model.Scraper, targetURL, outputDir string, diskCheck bool) error {
	fmt.Printf("Scraping %s...\n", targetURL)

	p, err := app.PreviewAlbum(context.Background(), scraper, targetURL, outputDir, diskCheck)
	if err != nil {
		return err
	}
	if p.Warning != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", p.Warning)
	}
	// Dry-run is informational: a URL that yields nothing prints the empty
	// message and exits 0.
	if len(p.Items) == 0 {
		fmt.Println("No items found.")
		return nil
	}
	if p.DiskWarning != "" {
		notice("%s\n", p.DiskWarning)
	}

	for _, item := range p.Items {
		sizeStr := "unknown size"
		if item.Size > 0 {
			sizeStr = fileutil.HumanBytes(item.Size)
		}
		fmt.Printf("  %-60s %s\n", model.TitleOf(item), sizeStr)
	}

	fmt.Println()
	if p.Unknown > 0 {
		fmt.Printf("%d items, %s total (%d with unknown size)\n", len(p.Items), fileutil.HumanBytes(p.TotalSize), p.Unknown)
	} else {
		fmt.Printf("%d items, %s total\n", len(p.Items), fileutil.HumanBytes(p.TotalSize))
	}
	return nil
}

// cliHooks routes app warnings through notice (held while the TUI runs)
// and scrape errors to the run log.
var cliHooks = app.Hooks{
	Notice: func(msg string) { notice("%s\n", msg) },
	Log:    func(msg string) { logLine("%s", msg) },
}

// printItemURLs writes one item URL per line to w, skipping items without
// a URL (nothing to fetch).
func printItemURLs(w io.Writer, items []model.MediaItem) {
	for _, item := range items {
		if item.URL == "" {
			continue
		}
		fmt.Fprintln(w, item.URL)
	}
}

func runDiskCheck(items []model.MediaItem, outputDir string, diskCheck bool) {
	if !diskCheck {
		return
	}
	if msg := app.DiskCheck(items, outputDir); msg != "" {
		notice("%s\n", msg)
	}
}
