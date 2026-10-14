package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

var version = "dev"

func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		logLine("error: %v", err)
		os.Exit(exitCode(err))
	}
}

// exitCode maps a run error to a process exit code: 1 when the whole run
// failed, 2 when only part of it did (some items or albums failed while
// others succeeded).
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var partial *model.PartialError
	if errors.As(err, &partial) {
		return 2
	}
	return 1
}

func newSiteCmd(use, short, site string) *cobra.Command {
	var urlFlag string

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targetURL, err := resolveURL(args, urlFlag, site)
			if err != nil {
				return err
			}
			opts, err := globalOptionsFromCommand(cmd)
			if err != nil {
				return err
			}
			return runSingle(opts, site, targetURL)
		},
	}

	cmd.Flags().StringVarP(&urlFlag, "url", "u", "", site+" URL")
	return cmd
}

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "scraper [url...]",
		Short: "Media scraper CLI tool",
		Long:  `Download media from various sites with a beautiful terminal UI.`,
		// Runtime errors (bad URL, network failure, etc.) shouldn't dump the
		// full usage/help text — just cobra's own clean "Error: ..." line.
		SilenceUsage: true,
		Args:         cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := globalOptionsFromCommand(cmd)
			if err != nil {
				return err
			}
			if helpRequested(args, opts.inputFileSet, stdinIsTerminal()) {
				return cmd.Help()
			}

			entries, err := collectBatchURLs(args, opts)
			if err != nil {
				return err
			}

			if opts.watch > 0 {
				opts = applyWatchDefaults(opts)
				return watchLoop(newRunOnce(opts, entries), opts.watch)
			}
			return newRunOnce(opts, entries)()
		},
	}

	root.PersistentFlags().String("config", "", "Config file path (default: ~/.scraper.yaml)")
	root.PersistentFlags().StringP("output", "o", "", "Output directory (default: ~/Downloads)")
	root.PersistentFlags().Int("concurrent", 3, "Maximum concurrent downloads")
	root.PersistentFlags().String("limit-rate", "", "Cap total download speed (e.g. 500K, 2M, 1G; default: unlimited)")
	root.PersistentFlags().Bool("checksums", false, "Write and verify .sha256 checksum sidecar files (default: off)")
	root.PersistentFlags().BoolP("dry-run", "n", false, "List items and total size without downloading anything")
	root.PersistentFlags().StringP("input-file", "i", "", "Read URLs from a file (one per line; optional tab-separated album URL)")
	root.PersistentFlags().Bool("no-tui", false, "Disable terminal UI; print plain text output")
	root.PersistentFlags().BoolP("json", "j", false, "Output JSON Lines (one event per line); implies --no-tui")
	root.PersistentFlags().String("proxy", "", "HTTP/S proxy URL for all requests (e.g. http://proxy:8080)")
	root.PersistentFlags().String("cookies", "", "Netscape-format cookies file (e.g. ~/cookies.txt)")
	root.PersistentFlags().String("quality", "", "Max YouTube resolution (2160p, 1440p, 1080p, 720p, 480p, 360p, 240p)")
	root.PersistentFlags().String("audio-only", "", "YouTube audio only; optional format: m4a (default) or mp3")
	// Bare --audio-only means m4a: the help text calls the format
	// optional, so the flag must work without a value.
	root.PersistentFlags().Lookup("audio-only").NoOptDefVal = "m4a"
	root.PersistentFlags().StringSlice("include", nil, "Keep only items matching glob (repeatable, e.g. --include '*.mp4' --include '*.mkv')")
	root.PersistentFlags().StringSlice("exclude", nil, "Skip items matching glob (repeatable)")
	root.PersistentFlags().StringSlice("allow-ext", nil, "Extra file extensions to keep with --media-only (repeatable, e.g. --allow-ext cbz)")
	root.PersistentFlags().Int("max-album-items", 0, "Cap items per album (0 = unlimited; default 10000)")
	root.PersistentFlags().Bool("print-urls", false, "Print item URLs (one per line) and exit without downloading")
	root.PersistentFlags().Duration("scrape-delay", 0, "Pause between album scrapes (e.g. 500ms; default: none)")
	root.PersistentFlags().String("log-file", "", "Append a timestamped run log (scrape errors, summaries) to this file")
	root.PersistentFlags().Duration("watch", 0, "Re-run the input list every interval until interrupted (implies --no-tui)")
	root.PersistentFlags().String("cookies-from-browser", "", "Import cookies for the supported sites from a local browser profile (firefox)")
	root.PersistentFlags().String("min-size", "", "Skip items smaller than size (e.g. 500K, 2M)")
	root.PersistentFlags().String("max-size", "", "Skip items larger than size (e.g. 500M, 1G)")
	root.PersistentFlags().Bool("disk-check", true, "Warn if free disk space is less than estimated download size")
	root.PersistentFlags().Bool("media-only", true, "Only download media and archive files (use --media-only=false to allow everything)")
	root.PersistentFlags().Bool("notify", false, "Send OS notification when downloads complete")

	root.AddCommand(
		newSiteCmd("youtube <url>", "Scrape and download a YouTube video or playlist", "youtube"),
		newSiteCmd("cyberdrop <url>", "Scrape and download a CyberDrop gallery", "cyberdrop"),
		newSiteCmd("bunker <url>", "Scrape and download Bunker content", "bunker"),
		newSiteCmd("goonbox <url>", "Scrape and download a GoonBox album", "goonbox"),
		newSiteCmd("filester <url>", "Scrape and download a Filester file or folder", "filester"),
		newSiteCmd("pixeldrain <url>", "Scrape and download a Pixeldrain list or single file", "pixeldrain"),
		newSiteCmd("turbo <url>", "Scrape and download a Turbo album", "turbo"),
		&cobra.Command{
			Use:   "version",
			Short: "Print the version",
			Run: func(cmd *cobra.Command, args []string) {
				fmt.Println("scraper", version)
			},
		},
		&cobra.Command{
			Use:   "completion [bash|zsh|fish|powershell]",
			Short: "Generate shell completion script",
			Long: `To load completions:

Bash:  source <(scraper completion bash)
Zsh:   source <(scraper completion zsh)
Fish:  scraper completion fish | source`,
			Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				switch args[0] {
				case "bash":
					return cmd.Root().GenBashCompletion(os.Stdout)
				case "zsh":
					return cmd.Root().GenZshCompletion(os.Stdout)
				case "fish":
					return cmd.Root().GenFishCompletion(os.Stdout, true)
				case "powershell":
					return cmd.Root().GenPowerShellCompletion(os.Stdout)
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "invalid completion shell %q\n\n%s", args[0], cmd.Root().UsageString())
				return fmt.Errorf("invalid completion shell %q", args[0])
			},
		},
	)

	return root
}
