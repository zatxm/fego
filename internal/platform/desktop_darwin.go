//go:build darwin && !ios && (amd64 || arm64)

package platform

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

func macIcon(img *image.NRGBA) objc.ID {
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		return 0
	}
	data := buffer.Bytes()
	// NSData copies before this call returns.
	nsData := macSend(macClass("NSData"), "dataWithBytes:length:", uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)))
	return macSend(macSend(macClass("NSImage"), "alloc"), "initWithData:", uintptr(nsData))
}

func (b *cocoaBackend) Configure(_, name string, icon *image.NRGBA) error {
	pool := mac.poolPush()
	defer mac.poolPop(pool)
	if name != "" {
		menu := macSend(b.app, "mainMenu")
		item := macSend(menu, "itemAtIndex:", 0)
		sub := macSend(item, "submenu")
		quit := macSend(sub, "itemAtIndex:", 0)
		macSend(item, "setTitle:", uintptr(macString(name)))
		macSend(sub, "setTitle:", uintptr(macString(name)))
		macSend(quit, "setTitle:", uintptr(macString("Quit "+name)))
	}
	if icon != nil {
		img := macIcon(icon)
		if img == 0 {
			return errors.New("fego: could not create the Dock icon")
		}
		macSend(b.app, "setApplicationIconImage:", uintptr(img))
		macSend(img, "release")
	}
	return nil
}

func (w *cocoaWindow) setIcons(o Options) {
	pool := mac.poolPush()
	defer mac.poolPop(pool)
	if o.Icon != nil {
		img := macIcon(o.Icon)
		macSend(w.window, "setMiniwindowImage:", uintptr(img))
		macSend(img, "release")
	}
	if o.TitleBarIcon == nil {
		return
	}
	img := macIcon(o.TitleBarIcon)
	view := macSend(macClass("NSImageView"), "imageViewWithImage:", uintptr(img))
	macSend(img, "release")
	mac.setSize(view, macSel("setFrameSize:"), NSSize{Width: 24, Height: 20})
	accessory := macSend(macClass("NSTitlebarAccessoryViewController"), "new")
	macSend(accessory, "setView:", uintptr(view))
	macSend(accessory, "setLayoutAttribute:", 2) // NSLayoutAttributeRight
	macSend(w.window, "addTitlebarAccessoryViewController:", uintptr(accessory))
	macSend(accessory, "release")
}

func (w *cocoaWindow) Show() { macSend(w.window, "orderFront:", 0) }
func (w *cocoaWindow) Hide() { macSend(w.window, "orderOut:", 0) }
func (w *cocoaWindow) Activate() {
	macSend(w.window, "deminiaturize:", 0)
	macSend(w.backend.app, "activateIgnoringOtherApps:", 1)
	macSend(w.window, "makeKeyAndOrderFront:", 0)
}

type cocoaTray struct {
	backend           *cocoaBackend
	target, bar, item objc.ID
	state             TrayState
	click             func(int32)
}

func (b *cocoaBackend) NewTray(state TrayState, click func(int32)) (Tray, error) {
	pool := mac.poolPush()
	defer mac.poolPop(pool)
	bar := macSend(macClass("NSStatusBar"), "systemStatusBar")
	var addStatus func(objc.ID, objc.SEL, float64) objc.ID
	purego.RegisterFunc(&addStatus, mac.msg)
	item := addStatus(bar, macSel("statusItemWithLength:"), -2)
	if item == 0 {
		return nil, errors.New("fego: could not create a menu bar item")
	}
	macSend(item, "retain")
	target := macSend(objc.ID(mac.delegateClass), "new")
	t := &cocoaTray{backend: b, target: target, bar: bar, item: item, click: click}
	if b.trays == nil {
		b.trays = make(map[objc.ID]*cocoaTray)
	}
	b.trays[target] = t
	if err := t.Update(state); err != nil {
		t.Close()
		return nil, err
	}
	return t, nil
}

func (t *cocoaTray) Update(s TrayState) error {
	pool := mac.poolPush()
	defer mac.poolPop(pool)
	button := macSend(t.item, "button")
	if s.Icon != t.state.Icon || s.TemplateIcon != t.state.TemplateIcon {
		img := macIcon(s.Icon)
		if img == 0 {
			return errors.New("fego: could not create a menu bar image")
		}
		// Status items use point sizes. Cap the height while preserving aspect ratio.
		w, h := s.Icon.Bounds().Dx(), s.Icon.Bounds().Dy()
		scale := min(20/float64(h), 48/float64(w))
		mac.setSize(img, macSel("setSize:"), NSSize{Width: float64(w) * scale, Height: float64(h) * scale})
		template := uintptr(0)
		if s.TemplateIcon {
			template = 1
		}
		macSend(img, "setTemplate:", template)
		macSend(button, "setImage:", uintptr(img))
		macSend(img, "release")
	}
	macSend(button, "setToolTip:", uintptr(macString(s.ToolTip)))
	if len(s.Menu) > 0 {
		menu := t.buildMenu(s.Menu)
		macSend(t.item, "setMenu:", uintptr(menu))
		macSend(menu, "release")
	} else {
		macSend(t.item, "setMenu:", 0)
		macSend(button, "setTarget:", uintptr(t.target))
		macSend(button, "setAction:", uintptr(macSel("fegoTrayActivate:")))
	}
	t.state = s
	return nil
}

func (t *cocoaTray) buildMenu(items []MenuItem) objc.ID {
	menu := macSend(macClass("NSMenu"), "new")
	macSend(menu, "setAutoenablesItems:", 0)
	for _, item := range items {
		if item.Separator {
			macSend(menu, "addItem:", uintptr(macSend(macClass("NSMenuItem"), "separatorItem")))
			continue
		}
		row := macSend(macSend(macClass("NSMenuItem"), "alloc"), "initWithTitle:action:keyEquivalent:",
			uintptr(macString(item.Label)), uintptr(macSel("fegoTrayAction:")), uintptr(macString("")))
		macSend(row, "setTarget:", uintptr(t.target))
		macSend(row, "setTag:", uintptr(item.ID))
		if item.Disabled {
			macSend(row, "setEnabled:", 0)
		}
		if item.Checkable && item.Checked {
			macSend(row, "setState:", 1)
		}
		if len(item.Submenu) > 0 {
			sub := t.buildMenu(item.Submenu)
			macSend(row, "setSubmenu:", uintptr(sub))
			macSend(sub, "release")
		}
		macSend(menu, "addItem:", uintptr(row))
		macSend(row, "release")
	}
	return menu
}

func (t *cocoaTray) Available() bool { return t.item != 0 }
func (t *cocoaTray) Close() {
	if t.item == 0 {
		return
	}
	pool := mac.poolPush()
	defer mac.poolPop(pool)
	delete(t.backend.trays, t.target)
	macSend(macSend(t.item, "button"), "setTarget:", 0)
	macSend(t.item, "setMenu:", 0)
	macSend(t.bar, "removeStatusItem:", uintptr(t.item))
	macSend(t.item, "release")
	macSend(t.target, "release")
	t.item = 0
}
