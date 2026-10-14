package app

import (
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/config"
	"github.com/rajbahakdanyu/scraper/internal/downloader"
)

// BuildDownloader must map the config's checksums setting to the
// downloader's skip flag: off by default, skipped unless enabled.
func TestBuildDownloaderChecksumsWiring(t *testing.T) {
	tests := []struct {
		name           string
		checksums      bool
		wantSkipChecks bool
	}{
		{"default off", false, true},
		{"enabled", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Checksums:           tt.checksums,
				ConcurrentDownloads: 1,
				Timeout:             30 * time.Second,
			}
			dl, err := BuildDownloader(nil, "generic", cfg, Hooks{})
			if err != nil {
				t.Fatal(err)
			}
			defer dl.Shutdown()

			concrete, ok := dl.(*downloader.Downloader)
			if !ok {
				t.Fatalf("BuildDownloader returned %T, want *downloader.Downloader", dl)
			}
			if got := concrete.SkipChecksums(); got != tt.wantSkipChecks {
				t.Fatalf("SkipChecksums() = %v, want %v", got, tt.wantSkipChecks)
			}
		})
	}
}
