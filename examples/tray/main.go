// The tray example runs without windows until the user opens one from the menu.
package main

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"log"

	"github.com/zatxm/fego/v26"
	"github.com/zatxm/fego/v26/ui"
)

func main() {
	icon := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	draw.Draw(icon, image.Rect(4, 4, 28, 28), image.NewUniform(color.NRGBA{R: 33, G: 118, B: 194, A: 255}), image.Point{}, draw.Src)
	app := fego.NewAppWithOptions(fego.AppOptions{
		ID: "org.example.fego.tray", Name: "Fego Tray Example", Icon: icon, KeepAlive: true,
	})
	var startErr error
	app.WhenReady(func() {
		var window *fego.Window
		open := func() {
			if window == nil {
				var err error
				window, err = app.NewWindow(fego.WindowOptions{
					Width: 360, Height: 180,
					Content: ui.View(ui.Column(ui.Text("The tray stays active when you close this window.")).Center().Padding(24)),
				})
				if err != nil {
					log.Print(err)
					return
				}
				window.OnClose(func(*fego.CloseEvent) { window = nil })
			}
			window.Activate()
		}
		tray, err := app.NewTray(fego.TrayOptions{Menu: fego.Menu{
			{Label: "Open window", OnClick: open},
			{Separator: true},
			{Label: "Quit", OnClick: app.Quit},
		}})
		if err == nil && !tray.Available() {
			err = errors.New("this desktop session has no active tray host")
		}
		if err != nil {
			startErr = err
			app.Quit()
		}
	})
	if err := errors.Join(app.Run(), startErr); err != nil {
		log.Fatal(err)
	}
}
