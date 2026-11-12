package backend

import (
	"github.com/wailsapp/wails/v3/pkg/application"
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
	Confirm(title, message string, onResult func(bool))
	HideWindow()
	Quit()
}

type wailsHost struct {
	app    *application.App
	window *application.WebviewWindow
}

func (h wailsHost) Emit(event string, data any) {
	h.app.Event.Emit(event, data)
}

func (h wailsHost) PickDirectory(title, dir string) (string, error) {
	return h.app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		Title:                title,
		Directory:            existingDir(dir),
		CanChooseDirectories: true,
		CanCreateDirectories: true,
		Window:               h.window,
	}).PromptForSingleSelection()
}

func (h wailsHost) PickFile(title string, filters []FileFilter) (string, error) {
	opts := &application.OpenFileDialogOptions{
		Title:          title,
		CanChooseFiles: true,
		Window:         h.window,
	}
	for _, filter := range filters {
		opts.Filters = append(opts.Filters, application.FileFilter{
			DisplayName: filter.DisplayName,
			Pattern:     filter.Pattern,
		})
	}
	return h.app.Dialog.OpenFileWithOptions(opts).PromptForSingleSelection()
}

func (h wailsHost) PickSaveFile(title, name string) (string, error) {
	return h.app.Dialog.SaveFileWithOptions(&application.SaveFileDialogOptions{
		Title:                title,
		Filename:             name,
		CanCreateDirectories: true,
		Window:               h.window,
	}).PromptForSingleSelection()
}

func (h wailsHost) Confirm(title, message string, onResult func(bool)) {
	dialog := h.app.Dialog.Question().
		SetTitle(title).
		SetMessage(message).
		AttachToWindow(h.window)
	dialog.AddButton("Quit").SetAsDefault().OnClick(func() { onResult(true) })
	dialog.AddButton("Cancel").SetAsCancel().OnClick(func() { onResult(false) })
	dialog.Show()
}

func (h wailsHost) HideWindow() {
	h.window.Hide()
}

func (h wailsHost) Quit() {
	h.app.Quit()
}
