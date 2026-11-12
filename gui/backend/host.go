package backend

import (
	"context"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// FileFilter restricts a file dialog, e.g. {"Text files", "*.txt"}.
type FileFilter struct {
	DisplayName string
	Pattern     string
}

// host is everything the backend needs from the desktop runtime, so the
// bound API can be tested without a window.
type host interface {
	Emit(event string, data any)
	PickDirectory(title, dir string) (string, error)
	PickFile(title string, filters []FileFilter) (string, error)
	PickSaveFile(title, name string) (string, error)
	Confirm(title, message string) bool
	HideWindow()
}

type wailsHost struct{ ctx context.Context }

func (h wailsHost) Emit(event string, data any) {
	runtime.EventsEmit(h.ctx, event, data)
}

func (h wailsHost) PickDirectory(title, dir string) (string, error) {
	return runtime.OpenDirectoryDialog(h.ctx, runtime.OpenDialogOptions{
		Title:                title,
		DefaultDirectory:     existingDir(dir),
		CanCreateDirectories: true,
	})
}

func (h wailsHost) PickFile(title string, filters []FileFilter) (string, error) {
	opts := runtime.OpenDialogOptions{Title: title}
	for _, f := range filters {
		opts.Filters = append(opts.Filters, runtime.FileFilter{DisplayName: f.DisplayName, Pattern: f.Pattern})
	}
	return runtime.OpenFileDialog(h.ctx, opts)
}

func (h wailsHost) PickSaveFile(title, name string) (string, error) {
	return runtime.SaveFileDialog(h.ctx, runtime.SaveDialogOptions{
		Title:                title,
		DefaultFilename:      name,
		CanCreateDirectories: true,
	})
}

func (h wailsHost) Confirm(title, message string) bool {
	answer, err := runtime.MessageDialog(h.ctx, runtime.MessageDialogOptions{
		Type:          runtime.QuestionDialog,
		Title:         title,
		Message:       message,
		Buttons:       []string{"Yes", "No"},
		DefaultButton: "No",
		CancelButton:  "No",
	})
	return err == nil && answer == "Yes"
}

func (h wailsHost) HideWindow() {
	runtime.WindowHide(h.ctx)
}
