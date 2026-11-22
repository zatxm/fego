package fego

import (
	"errors"
	"image"
	"math"

	"github.com/zatxm/fego/v26/internal/platform"
)

// Menu is a reusable snapshot of menu items. Commit changes with Tray.SetMenu.
type Menu []MenuItem

type MenuItem struct {
	Label     string
	Separator bool
	Disabled  bool
	Checkable bool
	Checked   bool
	Submenu   Menu
	OnClick   func()
}

type TrayOptions struct {
	Icon         image.Image // nil uses the application's default icon
	TemplateIcon bool        // macOS tints monochrome icons to match the menu bar
	Title        string
	ToolTip      string
	Menu         Menu
	OnClick      func() // primary activation where the desktop exposes it
}

type Tray struct {
	app      *Application
	native   platform.Tray
	state    platform.TrayState
	actions  map[int32]func()
	activate func()
	nextID   int32
}

// NewTray creates a tray on the UI thread, from WhenReady or a callback.
func (a *Application) NewTray(o TrayOptions) (*Tray, error) {
	b, ok := a.backend.(platform.DesktopBackend)
	if !ok || a.stopped || a.quitting {
		return nil, errors.New("fego: create trays from WhenReady or a UI callback")
	}
	icon := a.icon
	if o.Icon != nil {
		var err error
		icon, err = platform.CopyIcon(o.Icon)
		if err != nil {
			return nil, err
		}
	}
	if icon == nil {
		return nil, errors.New("fego: a tray icon is required")
	}
	if o.Title == "" {
		o.Title = a.options.Name
	}
	if o.ToolTip == "" {
		o.ToolTip = o.Title
	}
	t := &Tray{app: a, activate: o.OnClick}
	t.state = platform.TrayState{ID: a.options.ID, Title: o.Title, ToolTip: o.ToolTip, Icon: icon, TemplateIcon: o.TemplateIcon, HasActivation: o.OnClick != nil}
	menu, actions, err := t.menu(o.Menu)
	if err != nil {
		return nil, err
	}
	t.state.Menu, t.actions = menu, actions
	t.native, err = b.NewTray(t.state, t.click)
	if err != nil {
		return nil, err
	}
	if a.trays == nil {
		a.trays = make(map[*Tray]struct{})
	}
	a.trays[t] = struct{}{}
	return t, nil
}

func (t *Tray) click(id int32) {
	t.app.Post(func() {
		if t.native == nil {
			return
		}
		if id == 0 {
			if t.activate != nil {
				t.activate()
			}
		} else if fn := t.actions[id]; fn != nil {
			fn()
		}
	})
}

func (t *Tray) menu(menu Menu) ([]platform.MenuItem, map[int32]func(), error) {
	actions := make(map[int32]func())
	var parse func(Menu, int, bool) ([]platform.MenuItem, error)
	parse = func(items Menu, depth int, disabled bool) ([]platform.MenuItem, error) {
		if depth > 32 || len(items) > 65535 || int64(t.nextID)+int64(len(items)) > math.MaxInt32 {
			return nil, errors.New("fego: menu is too large or deeply nested")
		}
		var out []platform.MenuItem
		if len(items) > 0 {
			out = make([]platform.MenuItem, 0, len(items))
		}
		for _, item := range items {
			if t.nextID == math.MaxInt32 {
				return nil, errors.New("fego: menu ID space exhausted")
			}
			t.nextID++
			entry := platform.MenuItem{ID: t.nextID, Label: item.Label, Separator: item.Separator,
				Disabled: item.Disabled || disabled, Checkable: item.Checkable, Checked: item.Checked}
			if len(item.Submenu) > 0 && !item.Separator {
				var err error
				entry.Submenu, err = parse(item.Submenu, depth+1, entry.Disabled)
				if err != nil {
					return nil, err
				}
			} else if !entry.Disabled && !entry.Separator && item.OnClick != nil {
				actions[entry.ID] = item.OnClick
			}
			out = append(out, entry)
		}
		return out, nil
	}
	items, err := parse(menu, 0, false)
	return items, actions, err
}

// SetMenu replaces the menu on the UI thread.
func (t *Tray) SetMenu(menu Menu) error {
	items, actions, err := t.menu(menu)
	if err != nil {
		return err
	}
	state := t.state
	state.Menu = items
	if err := t.update(state); err != nil {
		return err
	}
	t.actions = actions
	return nil
}

// SetIcon replaces the icon on the UI thread.
func (t *Tray) SetIcon(icon image.Image, template bool) error {
	img, err := platform.CopyIcon(icon)
	if err != nil {
		return err
	}
	if img == nil {
		return errors.New("fego: a tray icon is required")
	}
	state := t.state
	state.Icon, state.TemplateIcon = img, template
	return t.update(state)
}

// SetToolTip changes the platform tooltip on the UI thread.
func (t *Tray) SetToolTip(text string) error {
	state := t.state
	state.ToolTip = text
	return t.update(state)
}

func (t *Tray) update(state platform.TrayState) error {
	if t.native == nil {
		return errors.New("fego: tray is closed")
	}
	if err := t.native.Update(state); err != nil {
		return err
	}
	t.state = state
	return nil
}

// Available reports whether the desktop has accepted the icon. Call on the UI thread.
func (t *Tray) Available() bool { return t.native != nil && t.native.Available() }

// Close removes the tray on the UI thread. Applications also close their trays on exit.
func (t *Tray) Close() {
	if t.native != nil {
		t.native.Close()
		t.native = nil
		t.state = platform.TrayState{}
		t.actions, t.activate = nil, nil
		delete(t.app.trays, t)
	}
}
