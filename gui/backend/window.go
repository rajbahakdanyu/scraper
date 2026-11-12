package backend

import (
	"context"
	goruntime "runtime"
	"sync/atomic"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// singleInstanceID identifies the app so a second launch focuses the
// running window instead of opening another one on the same config.
const singleInstanceID = "com.rajbahakdanyu.scraper-gui"

// appMenu is the standard macOS menu bar. Without it the webview gets no
// Edit menu, so ⌘C/⌘V/⌘Z and ⌘Q/⌘H/⌘M do nothing. Other platforms keep
// a menu-less window.
func appMenu() *menu.Menu {
	if goruntime.GOOS != "darwin" {
		return nil
	}
	return menu.NewMenuFromItems(menu.AppMenu(), menu.EditMenu(), menu.WindowMenu())
}

// nativeOptions makes the window behave like a native app: dark title bar
// chrome matching the UI, no pinch-zoom, and a single running instance.
func nativeOptions(ctx *atomic.Value) (*mac.Options, *windows.Options, *options.SingleInstanceLock) {
	focus := func(options.SecondInstanceData) {
		c, ok := ctx.Load().(context.Context)
		if !ok {
			return
		}
		runtime.WindowUnminimise(c)
		runtime.WindowShow(c)
	}
	return &mac.Options{Appearance: mac.NSAppearanceNameDarkAqua},
		&windows.Options{Theme: windows.Dark, DisablePinchZoom: true},
		&options.SingleInstanceLock{UniqueId: singleInstanceID, OnSecondInstanceLaunch: focus}
}
