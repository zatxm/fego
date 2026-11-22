//go:build !js && (!linux || android) && !windows && (!darwin || ios)

package ui

import (
	"errors"

	"github.com/zatxm/fego/v26"
)

func (*VideoPlayer) play(*fego.Window) error {
	return errors.New("ui: video playback is unavailable on this platform")
}
