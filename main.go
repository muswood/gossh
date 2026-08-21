// owner: muswood | Email: mumu920@outlook.com
package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "GoSSH",
		Width:     1400,
		Height:    900,
		MinWidth:  1024,
		MinHeight: 720,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour:            &options.RGBA{R: 15, G: 23, B: 42, A: 255},
		OnStartup:                   app.startup,
		OnShutdown:                  app.shutdown,
		Bind: []interface{}{
			app,
		},
		StartHidden:                 false,
		HideWindowOnClose:           false,
		EnableDefaultContextMenu:    true,
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
		},
		Mac: &mac.Options{
			TitleBar: &mac.TitleBar{
				HideTitle:            false,
				HideTitleBar:         false,
				FullSizeContent:      false,
				UseToolbar:           true,
				HideToolbarSeparator: true,
			},
			Appearance:           mac.NSAppearanceNameDarkAqua,
			WebviewIsTransparent: true,
			WindowIsTranslucent:  false,
		},
	})

	if err != nil {
		println("错误:", err.Error())
	}
}
