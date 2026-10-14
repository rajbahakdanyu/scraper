package backend

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/rajbahakdanyu/scraper/internal/app"
	"github.com/rajbahakdanyu/scraper/internal/session"
)

var textFilters = []FileFilter{{DisplayName: "Text files (*.txt)", Pattern: "*.txt"}, {DisplayName: "All files", Pattern: "*"}}

// ChooseOutputDir asks for the download folder; "" when cancelled.
func (a *App) ChooseOutputDir() (string, error) {
	return a.host.PickDirectory("Choose output folder", a.GetSettings().OutputDir)
}

// ChooseCookiesFile asks for a Netscape-format cookies file.
func (a *App) ChooseCookiesFile() (string, error) {
	return a.host.PickFile("Choose cookies file", textFilters)
}

// ChooseLogFile asks where to append the run log.
func (a *App) ChooseLogFile() (string, error) {
	return a.host.PickSaveFile("Choose log file", "scraper.log")
}

// ImportURLList reads a URL list (or retry list) file and returns its
// entries as input text, one "URL[<TAB>album URL]" per line.
func (a *App) ImportURLList() (string, error) {
	path, err := a.host.PickFile("Import URL list", textFilters)
	if err != nil || path == "" {
		return "", err
	}
	entries, err := app.ReadURLFile(path)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(entries))
	for _, e := range entries {
		line := e.URL
		if e.AlbumURL != "" {
			line += "\t" + e.AlbumURL
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

// OpenOutputFolder opens the run's folder (or the configured output
// folder) in the file manager.
func (a *App) OpenOutputFolder() error {
	dir := a.GetSettings().OutputDir
	a.withSession(func(_ *runner, s *session.Session) { dir = s.Dest })
	return openPath(dir, false)
}

// RevealItem shows item index's saved file, or its folder, in the file
// manager.
func (a *App) RevealItem(index int) error {
	var target string
	reveal := false
	a.withSession(func(_ *runner, s *session.Session) {
		if index < 0 || index >= len(s.Items) || s.Items[index].Header {
			return
		}
		st := s.Items[index]
		switch {
		case st.Path != "":
			target, reveal = st.Path, true
		case st.Dest != "":
			target = st.Dest
		default:
			target = s.Dest
		}
	})
	if target == "" {
		return errors.New("no such item")
	}
	return openPath(target, reveal)
}

// RevealRetryList shows a written retry list in the file manager.
func (a *App) RevealRetryList(path string) error {
	if !filepath.IsAbs(path) || !strings.HasPrefix(filepath.Base(path), "scraper-failed-") || filepath.Ext(path) != ".txt" {
		return fmt.Errorf("not a retry list: %q", path)
	}
	return openPath(path, true)
}

// openPath opens target with the OS file manager, or selects it there
// when reveal is set. Only existing absolute paths are passed, as
// arguments to a fixed program, never through a shell.
func openPath(target string, reveal bool) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return err
	}
	cmd := openCommand(goruntime.GOOS, abs, reveal)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open %s: %w", abs, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func openCommand(goos, abs string, reveal bool) *exec.Cmd {
	switch goos {
	case "darwin":
		if reveal {
			// #nosec G204 -- fixed binary; abs is an existing absolute path.
			return exec.Command("open", "-R", abs)
		}
		// #nosec G204 -- fixed binary; abs is an existing absolute path.
		return exec.Command("open", abs)
	case "windows":
		if reveal {
			// #nosec G204 -- fixed binary; abs is an existing absolute path.
			return exec.Command("explorer", "/select,"+abs)
		}
		// #nosec G204 -- fixed binary; abs is an existing absolute path.
		return exec.Command("explorer", abs)
	default:
		if reveal {
			abs = filepath.Dir(abs)
		}
		// #nosec G204 -- fixed binary; abs is an existing absolute path.
		return exec.Command("xdg-open", abs)
	}
}

func existingDir(dir string) string {
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return dir
	}
	return ""
}
