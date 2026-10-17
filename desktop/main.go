// Ferry for the desktop: Ferry in its own window on macOS, Windows and Linux, for anyone who'd rather
// not start it from a terminal. It runs Ferry (or finds the copy already running) and shows its page.
package main

import (
	"embed"
	"log"
	"runtime"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var icon []byte

func main() {
	app := NewApp()
	err := wails.Run(&options.App{
		Title:     "Ferry",
		Width:     1080,
		Height:    760,
		MinWidth:  420,
		MinHeight: 560,
		// Ferry's paper colour, so the window doesn't flash white before the page draws.
		BackgroundColour: &options.RGBA{R: 0xF3, G: 0xF1, B: 0xEC, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		Menu:             appMenu(app),
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "app.ferry.desktop",
			OnSecondInstanceLaunch: app.secondInstance,
		},
		Mac: &mac.Options{
			About: &mac.AboutInfo{Title: "Ferry", Message: "Photos, videos and files between your phone and your computer, over your own Wi-Fi.", Icon: icon},
		},
		Windows: &windows.Options{Theme: windows.SystemDefault},
		Linux:   &linux.Options{ProgramName: "Ferry", Icon: icon},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// appMenu is the Mac's menu bar: the usual app and Edit menus (without Edit, ⌘V wouldn't reach the
// page) and File › Send Files. Windows and Linux use the page's own buttons.
func appMenu(app *App) *menu.Menu {
	if runtime.GOOS != "darwin" {
		return nil
	}
	m := menu.NewMenu()
	m.Append(menu.AppMenu())
	file := m.AddSubmenu("File")
	file.AddText("Send Files…", keys.CmdOrCtrl("o"), func(*menu.CallbackData) { app.SendFiles() })
	file.AddText("Open the Ferry Folder", keys.Combo("o", keys.CmdOrCtrlKey, keys.ShiftKey), func(*menu.CallbackData) { app.OpenFolder() })
	file.AddSeparator()
	file.AddText("Open in Browser", nil, func(*menu.CallbackData) { app.OpenInBrowser() })
	m.Append(menu.EditMenu())
	m.Append(menu.WindowMenu())
	return m
}
