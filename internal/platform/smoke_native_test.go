//go:build (windows || (darwin && !ios)) && (amd64 || arm64)

package platform

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("FEGO_NATIVE_SMOKE_CHILD") == "1" {
		runtime.LockOSThread()
		if err := nativeSmoke(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestNativeWindowSmoke(t *testing.T) {
	if os.Getenv("FEGO_NATIVE_SMOKE") != "1" {
		t.Skip("set FEGO_NATIVE_SMOKE=1 in a desktop session")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), "FEGO_NATIVE_SMOKE_CHILD=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native smoke: %v\n%s", err, output)
	}
}

func nativeSmoke() error {
	var backend Backend
	var window Window
	var checkErr error
	phase := 0
	resized := false
	backend, checkErr = New(func() {
		if phase == 2 {
			window.Close()
			backend.Quit()
			return
		}
		if phase != 0 {
			return
		}
		frame := image.NewRGBA(image.Rect(0, 0, 320, 200))
		draw.Draw(frame, frame.Bounds(), image.NewUniform(color.RGBA{R: 37, G: 99, B: 235, A: 255}), image.Point{}, draw.Src)
		window.Present(frame, []image.Rectangle{frame.Bounds()})
		window.SetTitle("fego native smoke ✓")
		window.TextInput(true, image.Rect(10, 10, 12, 30))
		window.TextInput(false, image.Rectangle{})
		phase = 1
	})
	if checkErr != nil {
		return checkErr
	}
	window, checkErr = backend.Open(Options{Title: "fego native smoke", Width: 320, Height: 200}, func(event Event) {
		if event.Kind == Resize && event.Width > 0 && event.Height > 0 && event.Scale > 0 {
			resized = true
		}
		if event.Kind == Paint && phase == 1 {
			phase = 2
			backend.Wake()
		}
	})
	if checkErr != nil {
		backend.Quit()
		return checkErr
	}
	go backend.Wake()
	if err := backend.Run(); err != nil {
		return err
	}
	if phase != 2 || !resized {
		return fmt.Errorf("missing paint or resize: phase=%d resize=%v", phase, resized)
	}
	return nil
}
