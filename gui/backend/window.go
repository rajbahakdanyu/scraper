package backend

import (
	goruntime "runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/icons"
)

const singleInstanceID = "com.rajbahakdanyu.scraper-gui"

func appMenu(app *application.App, window *application.WebviewWindow, a *App) *application.Menu {
	menu := app.NewMenu()
	if goruntime.GOOS == "darwin" {
		menu.AddRole(application.AppMenu)
	}
	menu.AddRole(application.FileMenu)
	menu.AddRole(application.EditMenu)

	windowMenu := menu.AddSubmenu("Window")
	windowMenu.Add("Show Scraper").OnClick(func(*application.Context) {
		window.Restore()
		window.Show()
		window.Focus()
	})
	windowMenu.AddRole(application.Minimise)
	windowMenu.AddRole(application.Zoom)
	windowMenu.AddRole(application.FullScreen)

	menu.AddRole(application.HelpMenu)
	return menu
}

func (a *App) setupTray(app *application.App, window *application.WebviewWindow, icon []byte) {
	tray := app.SystemTray.New()
	if goruntime.GOOS == "darwin" {
		tray.SetTemplateIcon(icons.SystrayMacTemplate)
	} else {
		tray.SetIcon(icon)
	}
	tray.SetTooltip("Scraper")
	tray.OnClick(func() {
		if window.IsVisible() && !window.IsMinimised() {
			window.Hide()
			return
		}
		window.Restore()
		window.Show()
		window.Focus()
	})
	tray.OnRightClick(func() { tray.OpenMenu() })

	menu := app.NewMenu()
	menu.Add("Show Scraper").OnClick(func(*application.Context) {
		window.Restore()
		window.Show()
		window.Focus()
	})
	menu.Add("Stop Watching").OnClick(func(*application.Context) {
		a.StopWatch()
	})
	menu.AddSeparator()
	menu.Add("Quit Scraper").OnClick(func(*application.Context) {
		if a.confirmQuit() {
			a.host.Quit()
		}
	})
	tray.SetMenu(menu)
}
