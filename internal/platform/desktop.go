package platform

import (
	"errors"
	"image"
	"image/draw"
)

// DesktopBackend provides optional application-level integration.
type DesktopBackend interface {
	Configure(id, name string, icon *image.NRGBA) error
	NewTray(TrayState, func(int32)) (Tray, error)
}

type WindowControls interface {
	Show()
	Hide()
	Activate()
}

type MenuItem struct {
	ID        int32
	Label     string
	Separator bool
	Disabled  bool
	Checkable bool
	Checked   bool
	Submenu   []MenuItem
}

type TrayState struct {
	ID, Title, ToolTip string
	Icon               *image.NRGBA
	TemplateIcon       bool
	HasActivation      bool
	Menu               []MenuItem
}

type Tray interface {
	Update(TrayState) error
	Available() bool
	Close()
}

func CopyIcon(src image.Image) (*image.NRGBA, error) {
	if src == nil {
		return nil, nil
	}
	b := src.Bounds()
	if b.Empty() || b.Dx() > 1024 || b.Dy() > 1024 {
		return nil, errors.New("fego: icons must be between 1 and 1024 pixels per side")
	}
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst, nil
}
