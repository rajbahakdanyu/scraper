package cmd

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

func TestNewRootCmdRegistersSubcommands(t *testing.T) {
	root := NewRootCmd()

	for site := range app.SiteDomains {
		var cmd *cobra.Command
		for _, c := range root.Commands() {
			if c.Name() == site {
				cmd = c
			}
		}
		if cmd == nil {
			t.Errorf("expected root command to register %q subcommand", site)
			continue
		}
		if cmd.Use != site+" <url>" {
			t.Errorf("%s: unexpected Use %q", site, cmd.Use)
		}
		if cmd.Flags().Lookup("url") == nil {
			t.Errorf("%s: expected --url flag", site)
		}
		if err := cmd.RunE(cmd, nil); err == nil {
			t.Errorf("%s: expected an error when no URL is provided", site)
		}
	}

	if root.PersistentFlags().Lookup("config") == nil {
		t.Error("expected --config persistent flag")
	}
	if root.PersistentFlags().Lookup("output") == nil {
		t.Error("expected --output persistent flag")
	}
	if root.PersistentFlags().Lookup("concurrent") == nil {
		t.Error("expected --concurrent persistent flag")
	}
	if root.PersistentFlags().Lookup("limit-rate") == nil {
		t.Error("expected --limit-rate persistent flag")
	}
	if root.PersistentFlags().Lookup("checksums") == nil {
		t.Error("expected --checksums persistent flag")
	}
	if root.PersistentFlags().Lookup("no-checksums") != nil {
		t.Error("--no-checksums must be gone: checksums are off by default")
	}
	if root.PersistentFlags().Lookup("dry-run") == nil {
		t.Error("expected --dry-run persistent flag")
	}
}

func TestCompletionRejectsUnknownShell(t *testing.T) {
	root := NewRootCmd()
	var stderr bytes.Buffer
	root.SetErr(&stderr)
	root.SetArgs([]string{"completion", "tcsh"})

	if _, err := root.ExecuteC(); err == nil {
		t.Fatal("expected unknown completion shell to return an error")
	}
	if !strings.Contains(stderr.String(), "tcsh") {
		t.Fatalf("expected error output to mention shell, got %q", stderr.String())
	}
}

func TestNewRootCmdRegistersAllowExtFlag(t *testing.T) {
	root := NewRootCmd()
	if root.PersistentFlags().Lookup("allow-ext") == nil {
		t.Error("expected --allow-ext persistent flag")
	}
	if root.PersistentFlags().Lookup("max-album-items") == nil {
		t.Error("expected --max-album-items persistent flag")
	}
	if root.PersistentFlags().Lookup("print-urls") == nil {
		t.Error("expected --print-urls persistent flag")
	}
}

func TestExitCodeMapping(t *testing.T) {
	if got := exitCode(nil); got != 0 {
		t.Fatalf("exitCode(nil) = %d, want 0", got)
	}
	if got := exitCode(errors.New("boom")); got != 1 {
		t.Fatalf("exitCode(total failure) = %d, want 1", got)
	}
	if got := exitCode(model.NewPartialError("download", []error{errors.New("one failed")})); got != 2 {
		t.Fatalf("exitCode(partial) = %d, want 2", got)
	}
}

func TestRootCmdNoArgsShowsHelp(t *testing.T) {
	// Bare invocation shows help only when stdin is an interactive terminal
	// (no piped URL source). Make that deterministic under `go test`.
	original := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	defer func() { stdinIsTerminal = original }()

	root := NewRootCmd()
	if err := root.RunE(root, nil); err != nil {
		t.Errorf("expected bare invocation to show help without error, got: %v", err)
	}
}

func TestRootCmdAutoDetectsUnknownDomain(t *testing.T) {
	root := NewRootCmd()
	if err := root.RunE(root, []string{"https://example.com/whatever"}); err == nil {
		t.Error("expected error for an unrecognized domain")
	}
}

func TestNewRootCmdRegistersInputFileFlag(t *testing.T) {
	root := NewRootCmd()
	flag := root.PersistentFlags().Lookup("input-file")
	if flag == nil {
		t.Error("expected --input-file persistent flag")
	}
	if flag.Shorthand != "i" {
		t.Errorf("expected --input-file shorthand -i, got %q", flag.Shorthand)
	}
}

func TestNewRootCmdRegistersNoTuiAndJSONFlags(t *testing.T) {
	root := NewRootCmd()
	for _, name := range []string{"no-tui", "json"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("expected --%s persistent flag", name)
		}
	}
}

func TestNewRootCmdHasProxyFlag(t *testing.T) {
	root := NewRootCmd()
	flag := root.PersistentFlags().Lookup("proxy")
	if flag == nil {
		t.Error("expected --proxy persistent flag")
	}
}

func TestNewRootCmdRegistersVersionAndCompletion(t *testing.T) {
	root := NewRootCmd()
	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	for _, name := range []string{"version", "completion"} {
		if !names[name] {
			t.Errorf("expected %q subcommand", name)
		}
	}
}

func TestNewRootCmdHasCookiesFlag(t *testing.T) {
	root := NewRootCmd()
	flag := root.PersistentFlags().Lookup("cookies")
	if flag == nil {
		t.Error("expected --cookies persistent flag")
	}
}

func TestSiteSubcommandScrapesThroughProxy(t *testing.T) {
	for _, tc := range []struct{ site, url, host string }{
		{"filester", "https://filester.sh/f/abc", "filester.sh:443"},
		{"youtube", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "www.youtube.com:443"},
	} {
		t.Run(tc.site, func(t *testing.T) {
			connects := make(chan string, 16)
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodConnect {
					select {
					case connects <- r.Host:
					default:
					}
				}
				http.Error(w, "blocked", http.StatusForbidden)
			}))
			defer proxy.Close()

			root := NewRootCmd()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs([]string{tc.site, "--config", emptyTestConfig(t), "--proxy", proxy.URL, "--dry-run", tc.url})
			captureStdout(func() { _ = root.Execute() })

			select {
			case host := <-connects:
				if host != tc.host {
					t.Fatalf("proxy saw CONNECT %q, want %q", host, tc.host)
				}
			default:
				t.Fatal("the subcommand's scrape request bypassed --proxy")
			}
		})
	}
}

func TestNewRootCmdHasQualityAndAudioOnlyFlags(t *testing.T) {
	root := NewRootCmd()
	for _, name := range []string{"quality", "audio-only"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("expected --%s persistent flag", name)
		}
	}
}

func TestNewRootCmdHasFilterFlags(t *testing.T) {
	root := NewRootCmd()
	for _, name := range []string{"include", "exclude", "min-size", "max-size"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("expected --%s persistent flag", name)
		}
	}
}

func TestNewRootCmdHasDiskCheckAndNotifyFlags(t *testing.T) {
	root := NewRootCmd()
	for _, name := range []string{"disk-check", "notify"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("expected --%s persistent flag", name)
		}
	}
}

func TestRootCommandReadsInputFileWithoutPositionalArgs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "urls.txt")
	if err := os.WriteFile(path, []byte("https://example.com/not-supported\n"), 0600); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{
		"--config", emptyTestConfig(t),
		"--input-file", path,
	})

	_, err := root.ExecuteC()
	if err == nil {
		t.Fatalf("expected the unsupported URL to fail, got help instead:\n%s", stdout.String())
	}
	if combined := stdout.String() + stderr.String(); strings.Contains(combined, "Usage:") {
		t.Fatalf("expected the command to run, but it printed help:\n%s", combined)
	}
}

func TestOutputFlagHelpMentionsEffectiveDefault(t *testing.T) {
	root := NewRootCmd()
	flag := root.PersistentFlags().Lookup("output")
	if flag == nil {
		t.Fatal("expected --output persistent flag")
	}
	if flag.DefValue != "" {
		t.Errorf("--output flag default = %q, want empty (the effective default lives in config.Load)", flag.DefValue)
	}
	if !strings.Contains(flag.Usage, "~/Downloads") {
		t.Errorf("--output usage = %q, want it to mention ~/Downloads", flag.Usage)
	}
}
