package fego

import (
	"image"
	"os"
	"runtime"
	"testing"
	"time"
)

func TestDesktopLifecycleX11(t *testing.T) {
	if os.Getenv("FEGO_X11_TEST") != "1" {
		t.Skip("set FEGO_X11_TEST=1 on a dedicated X display")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	app := NewAppWithOptions(AppOptions{
		ID: "org.example.fego.test", Name: "Test", Icon: image.NewRGBA(image.Rect(0, 0, 16, 16)),
		KeepAlive: true,
	})
	done := false
	timer := time.AfterFunc(5*time.Second, app.Quit)
	defer timer.Stop()
	app.WhenReady(func() {
		app.Post(func() {
			first, err := app.NewWindow(WindowOptions{Title: "Same title", Content: &testContent{}})
			if err != nil {
				t.Error(err)
				app.Quit()
				return
			}
			second, err := app.NewWindow(WindowOptions{Title: "Same title", Content: &testContent{}})
			if err != nil {
				t.Error(err)
				app.Quit()
				return
			}
			second.OnClose(func(e *CloseEvent) {
				e.PreventDefault()
				second.Hide()
			})
			first.Hide()
			second.Close()
			second.Activate()
			app.Post(func() {
				if first.closed || second.closed {
					t.Error("close veto or activation unexpectedly closed a window")
				}
				first.Destroy()
				second.Destroy()
				app.Post(func() {
					if len(app.windows) != 0 || app.quitting {
						t.Error("destroying both windows should have left the app running")
					}
					done = true
					app.Quit()
				})
			})
		})
	})
	if err := app.Run(); err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Fatal("desktop event loop did not complete")
	}
}
