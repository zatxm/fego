//go:build (linux && !android) || windows || (darwin && !ios)

package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/zatxm/fego/v26"
)

func (v *VideoPlayer) play(w *fego.Window) error {
	path, err := filepath.Abs(v.source)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("ui: video source must be a regular file")
	}
	ctx, cancel := context.WithCancel(context.Background())
	width, height := v.pixelSize.X, v.pixelSize.Y
	filter := fmt.Sprintf("fps=30,scale=%d:%d:force_original_aspect_ratio=decrease,format=rgba,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", width, height, width, height)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-nostdin", "-threads", "1", "-filter_threads", "1", "-re",
		"-protocol_whitelist", "file,pipe", "-i", path, "-map", "0:v:0", "-an", "-vf", filter, "-pix_fmt", "rgba", "-threads", "1", "-f", "rawvideo", "pipe:1")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return err
	}
	if err = cmd.Start(); err != nil {
		cancel()
		return err
	}
	v.stop = cancel
	done := w.Done()
	var reused *image.RGBA
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
			cancel()
		}
	}()
	go func() {
		bounds := image.Rect(0, 0, width, height)
		buffer := image.NewRGBA(bounds)
		ack := make(chan struct{}, 1)
		var decodeErr error
		for {
			_, decodeErr = io.ReadFull(pipe, buffer.Pix)
			if decodeErr != nil {
				break
			}
			w.Update(func() {
				if ctx.Err() == nil {
					v.showFrame(buffer)
				}
				ack <- struct{}{}
			})
			select {
			case <-ctx.Done():
			case <-ack:
				if reused == nil || reused.Bounds() != bounds {
					reused = image.NewRGBA(bounds)
				}
				buffer, reused = reused, buffer
				continue
			}
			break
		}
		waitErr := cmd.Wait()
		if ctx.Err() != nil {
			return
		}
		if errors.Is(decodeErr, io.EOF) {
			decodeErr = nil
		}
		w.Update(func() {
			if ctx.Err() == nil {
				v.videoEnded(errors.Join(decodeErr, waitErr))
			}
		})
	}()
	return nil
}
