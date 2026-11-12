package backend

import (
	"context"
	goruntime "runtime"
	"sync/atomic"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// singleInstanceID identifies the app so a second launch focuses the
// running window instead of opening another one on the same config.
const singleInstanceID = "com.rajbahakdanyu.scraper-gui"

// appMenu provides standard App and Edit menus plus a Window menu that can
// restore a hidden window. Other platforms keep a menu-less window.
func appMenu(ctx *atomic.Value) *menu.Menu {
	if goruntime.GOOS != "darwin" {
		return nil
	}
	window := menu.NewMenu()
	window.AddText("Show Scraper", nil, func(*menu.CallbackData) {
		c, ok := ctx.Load().(context.Context)
		if !ok {
			return
		}
		runtime.WindowUnminimise(c)
		runtime.WindowShow(c)
	})
	window.AddSeparator()
	window.AddText("Minimize", keys.CmdOrCtrl("m"), func(*menu.CallbackData) {
		if c, ok := ctx.Load().(context.Context); ok {
			runtime.WindowMinimise(c)
		}
	})
	window.AddText("Zoom", nil, func(*menu.CallbackData) {
		if c, ok := ctx.Load().(context.Context); ok {
			runtime.WindowToggleMaximise(c)
		}
	})
	window.AddText("Toggle Full Screen", keys.Combo("f", keys.ControlKey, keys.CmdOrCtrlKey), func(*menu.CallbackData) {
		if c, ok := ctx.Load().(context.Context); ok {
			if runtime.WindowIsFullscreen(c) {
				runtime.WindowUnfullscreen(c)
			} else {
				runtime.WindowFullscreen(c)
			}
		}
	})
	return menu.NewMenuFromItems(menu.AppMenu(), menu.EditMenu(), menu.SubMenu("Window", window))
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
