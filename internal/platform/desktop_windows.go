//go:build windows && (amd64 || arm64)

package platform

import (
	"fmt"
	"image"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/image/draw"
)

var (
	winSetAppID        = syscallShell.NewProc("SetCurrentProcessExplicitAppUserModelID")
	syscallShell       = syscall.NewLazyDLL("shell32.dll")
	winShow            = winUser.NewProc("ShowWindow")
	winForeground      = winUser.NewProc("SetForegroundWindow")
	winIsIconic        = winUser.NewProc("IsIconic")
	winSend            = winUser.NewProc("SendMessageW")
	winMakeDIB         = winGDI.NewProc("CreateDIBSection")
	winMakeBitmap      = winGDI.NewProc("CreateBitmap")
	winDeleteGDI       = winGDI.NewProc("DeleteObject")
	winIconFromBitmap  = winUser.NewProc("CreateIconIndirect")
	winDestroyIcon     = winUser.NewProc("DestroyIcon")
	winNotifyIcon      = syscallShell.NewProc("Shell_NotifyIconW")
	winRegisterMessage = winUser.NewProc("RegisterWindowMessageW")
	winCreateMenu      = winUser.NewProc("CreatePopupMenu")
	winDestroyMenu     = winUser.NewProc("DestroyMenu")
	winAppendMenu      = winUser.NewProc("AppendMenuW")
	winTrackMenu       = winUser.NewProc("TrackPopupMenuEx")
	winGetCursor       = winUser.NewProc("GetCursorPos")
	winMetrics         = winUser.NewProc("GetSystemMetrics")
)

func (b *windowsBackend) Configure(id, _ string, _ *image.NRGBA) error {
	if id == "" {
		return nil
	}
	if len(utf16.Encode([]rune(id))) > 128 || strings.ContainsRune(id, 0) {
		return fmt.Errorf("fego: invalid Windows application ID")
	}
	text := winUTF16(id)
	if result, _, _ := winSetAppID.Call(uintptr(unsafe.Pointer(&text[0]))); int32(result) < 0 {
		return fmt.Errorf("set application ID: HRESULT %#x", uint32(result))
	}
	return nil
}

func (w *windowsWindow) Show() { winShow.Call(w.hwnd, 8) } // SW_SHOWNA
func (w *windowsWindow) Hide() { winShow.Call(w.hwnd, 0) }
func (w *windowsWindow) Activate() {
	if minimized, _, _ := winIsIconic.Call(w.hwnd); minimized != 0 {
		winShow.Call(w.hwnd, 9)
	} else {
		w.Show()
	}
	winForeground.Call(w.hwnd)
}

func (w *windowsWindow) setIcon(img *image.NRGBA) error {
	for i, metric := range []uintptr{49, 11} {
		size, _, _ := winMetrics.Call(metric)
		icon, err := winMakeIcon(img, int(size))
		if err != nil {
			return err
		}
		w.icons[i] = icon
		winSend.Call(w.hwnd, 0x80, uintptr(i), icon)
	}
	return nil
}

func (w *windowsWindow) releaseIcons() {
	for i, icon := range w.icons {
		if icon != 0 {
			winDestroyIcon.Call(icon)
			w.icons[i] = 0
		}
	}
}

func winMakeIcon(src *image.NRGBA, size int) (uintptr, error) {
	if size <= 0 {
		size = 32
	}
	bounds := image.Rect(0, 0, size, size)
	// RGBA stores premultiplied channels, as required by Win32's alpha icon.
	rgba := image.NewRGBA(bounds)
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	if w >= h {
		rgbaHeight := max(1, size*h/w)
		bounds.Min.Y, bounds.Max.Y = (size-rgbaHeight)/2, (size-rgbaHeight)/2+rgbaHeight
	} else {
		rgbaWidth := max(1, size*w/h)
		bounds.Min.X, bounds.Max.X = (size-rgbaWidth)/2, (size-rgbaWidth)/2+rgbaWidth
	}
	draw.ApproxBiLinear.Scale(rgba, bounds, src, src.Bounds(), draw.Src, nil)
	header := winBitmap{Size: 40, Width: int32(size), Height: -int32(size), Planes: 1, Bits: 32}
	var bits *byte
	color, _, err := winMakeDIB.Call(0, uintptr(unsafe.Pointer(&header)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if color == 0 {
		return 0, fmt.Errorf("create icon bitmap: %w", err)
	}
	defer winDeleteGDI.Call(color)
	output := unsafe.Slice(bits, len(rgba.Pix))
	maskStride := ((size + 15) / 16) * 2
	maskBits := make([]byte, size*maskStride)
	for i := range size * size {
		p := rgba.Pix[i*4:]
		copy(output[i*4:], []byte{p[2], p[1], p[0], p[3]})
		if p[3] == 0 {
			x, y := i%size, i/size
			maskBits[y*maskStride+x/8] |= 0x80 >> (x % 8)
		}
	}
	mask, _, err := winMakeBitmap.Call(uintptr(size), uintptr(size), 1, 1, uintptr(unsafe.Pointer(&maskBits[0])))
	if mask == 0 {
		return 0, fmt.Errorf("create icon mask: %w", err)
	}
	defer winDeleteGDI.Call(mask)
	info := struct {
		Icon, X, Y  uint32
		Mask, Color uintptr
	}{Icon: 1, Mask: mask, Color: color}
	icon, _, err := winIconFromBitmap.Call(uintptr(unsafe.Pointer(&info)))
	if icon == 0 {
		return 0, fmt.Errorf("create native icon: %w", err)
	}
	return icon, nil
}

const winTrayMessage = 0x8002

type winNotifyData struct {
	Size               uint32
	Window             uintptr
	ID, Flags, Message uint32
	Icon               uintptr
	Tip                [128]uint16
	State, StateMask   uint32
	Info               [256]uint16
	Version            uint32
	InfoTitle          [64]uint16
	InfoFlags          uint32
	GUID               [16]byte
	BalloonIcon        uintptr
}

type windowsTray struct {
	backend   *windowsBackend
	data      winNotifyData
	state     TrayState
	click     func(int32)
	available bool
}

func (b *windowsBackend) NewTray(s TrayState, click func(int32)) (Tray, error) {
	if b.taskbarCreated == 0 {
		name := winUTF16("TaskbarCreated")
		id, _, err := winRegisterMessage.Call(uintptr(unsafe.Pointer(&name[0])))
		if id == 0 {
			return nil, fmt.Errorf("register TaskbarCreated: %w", err)
		}
		b.taskbarCreated = uint32(id)
		b.trays = make(map[uintptr]*windowsTray)
	}
	t := &windowsTray{backend: b, click: click}
	// A hidden top-level window receives Explorer's broadcast even when there
	// are no visible application windows.
	name := winUTF16("FegoNativeWindow")
	hwnd, _, err := winCreate.Call(0, uintptr(unsafe.Pointer(&name[0])), 0, 0x80000000, 0, 0, 0, 0, 0, 0, b.instance, 0)
	if hwnd == 0 {
		return nil, fmt.Errorf("create tray message window: %w", err)
	}
	b.trays[hwnd] = t
	t.data = winNotifyData{Window: hwnd, ID: 1, Flags: 1 | 2 | 4 | 0x80, Message: winTrayMessage}
	t.data.Size = uint32(unsafe.Sizeof(t.data))
	if err := t.Update(s); err != nil {
		t.Close()
		return nil, err
	}
	return t, nil
}

func (t *windowsTray) Update(s TrayState) error {
	data := t.data
	if t.state.Icon != s.Icon {
		size, _, _ := winMetrics.Call(49)
		icon, err := winMakeIcon(s.Icon, int(size))
		if err != nil {
			return err
		}
		data.Icon = icon
	}
	data.Tip = [128]uint16{}
	tip := utf16.Encode([]rune(s.ToolTip))
	if len(tip) >= len(data.Tip) {
		tip = tip[:len(data.Tip)-1]
		if tip[len(tip)-1] >= 0xd800 && tip[len(tip)-1] <= 0xdbff {
			tip = tip[:len(tip)-1]
		}
	}
	copy(data.Tip[:], tip)
	command := uintptr(0)
	if t.available {
		command = 1
	}
	if ok, _, err := winNotifyIcon.Call(command, uintptr(unsafe.Pointer(&data))); ok == 0 {
		if data.Icon != t.data.Icon {
			winDestroyIcon.Call(data.Icon)
		}
		return fmt.Errorf("register or update notification icon: %w", err)
	}
	if command == 0 {
		data.Version = 4
		winNotifyIcon.Call(4, uintptr(unsafe.Pointer(&data)))
	}
	if t.data.Icon != 0 && t.data.Icon != data.Icon {
		winDestroyIcon.Call(t.data.Icon)
	}
	t.available = true
	t.data, t.state = data, s
	return nil
}

func makeWinMenu(items []MenuItem) (uintptr, error) {
	menu, _, err := winCreateMenu.Call()
	if menu == 0 {
		return 0, fmt.Errorf("create tray menu: %w", err)
	}
	for _, item := range items {
		var flag uintptr
		id := uintptr(item.ID)
		if item.Separator {
			flag = 0x800
		}
		if item.Disabled {
			flag |= 3
		}
		if item.Checkable && item.Checked {
			flag |= 8
		}
		if len(item.Submenu) > 0 {
			sub, err := makeWinMenu(item.Submenu)
			if err != nil {
				winDestroyMenu.Call(menu)
				return 0, err
			}
			id, flag = sub, flag|0x10
		}
		label := winUTF16(strings.ReplaceAll(item.Label, "&", "&&"))
		if ok, _, err := winAppendMenu.Call(menu, flag, id, uintptr(unsafe.Pointer(&label[0]))); ok == 0 {
			if flag&0x10 != 0 {
				winDestroyMenu.Call(id)
			}
			winDestroyMenu.Call(menu)
			return 0, fmt.Errorf("append tray menu item: %w", err)
		}
	}
	return menu, nil
}

func (t *windowsTray) handle(message uint32, _, lp uintptr) bool {
	switch message {
	case t.backend.taskbarCreated:
		t.available = false
		if ok, _, _ := winNotifyIcon.Call(0, uintptr(unsafe.Pointer(&t.data))); ok != 0 {
			winNotifyIcon.Call(4, uintptr(unsafe.Pointer(&t.data)))
			t.available = true
		}
	case winTrayMessage:
		switch uint16(lp) {
		case 0x7b, 0x205: // WM_CONTEXTMENU, or legacy WM_RBUTTONUP
			t.popup()
		case 0x400, 0x401, 0x202: // NIN_SELECT, NIN_KEYSELECT, or legacy WM_LBUTTONUP
			if t.state.HasActivation {
				t.click(0)
			} else {
				t.popup()
			}
		}
	default:
		return false
	}
	return true
}

func (t *windowsTray) popup() {
	if len(t.state.Menu) == 0 {
		return
	}
	menu, err := makeWinMenu(t.state.Menu)
	if err != nil {
		return
	}
	defer winDestroyMenu.Call(menu)
	var pos winPoint
	winGetCursor.Call(uintptr(unsafe.Pointer(&pos)))
	hwnd := t.data.Window
	winForeground.Call(hwnd)
	const returnCommand = 0x100
	cmd, _, _ := winTrackMenu.Call(menu, returnCommand|2, uintptr(pos.X), uintptr(pos.Y), hwnd, 0)
	winPost.Call(hwnd, 0, 0, 0)
	if cmd != 0 {
		t.click(int32(cmd))
	}
}

func (t *windowsTray) Available() bool { return t.available }
func (t *windowsTray) Close() {
	if t.data.Window == 0 {
		return
	}
	delete(t.backend.trays, t.data.Window)
	if t.available {
		winNotifyIcon.Call(2, uintptr(unsafe.Pointer(&t.data)))
		t.available = false
	}
	if t.data.Icon != 0 {
		winDestroyIcon.Call(t.data.Icon)
	}
	winDestroy.Call(t.data.Window)
	t.data.Window = 0
}
