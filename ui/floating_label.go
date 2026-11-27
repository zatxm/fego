package ui

// FloatingLabel shows title inside an empty text input, text area or select.
// A value or a focused placeholder moves the title into the top outline. Pass "" to restore
// the original placeholder, outline and accessible name.
func (e *Element) FloatingLabel(title string) *Element {
	if e.style.tone == groupSurface {
		for _, c := range e.children {
			if (c.kind == kInput && c.otpLength == 0) || c.kind == kSelect {
				c.FloatingLabel(title)
				return e
			}
		}
	}
	if (e.kind != kInput && e.kind != kSelect) || e.otpLength > 0 {
		panic("ui: FloatingLabel requires a text input, text area or select")
	}
	if e.floatingLabel != title {
		layout := (title == "") != (e.floatingLabel == "")
		e.own().floatingLabel = title
		e.touch(layout)
	}
	return e
}

const floatingLabelInset = 10

// InputGroup owns the outline around the editor and its addons.
func (e *Element) captionControl() *Element {
	if e.style.tone == groupSurface {
		for _, c := range e.children {
			if !c.hidden && c.floatingLabel != "" {
				return c
			}
		}
	}
	if e.floatingLabel != "" && (e.parent == nil || e.parent.style.tone != groupSurface) {
		return e
	}
	return nil
}

func selectTextBox(r Rect) Rect {
	return Rect{r.X + 12, r.Y, max(0, r.W-36), r.H}
}

func (e *Element) selectLabel(i int) (string, bool) {
	if i >= 0 && i < len(e.options) && (e.floatingLabel == "" || e.options[i] != "") {
		return e.options[i], true
	}
	return e.emptyFieldText(), false
}

func (e *Element) hasOptionText(i int) bool {
	return i >= 0 && i < len(e.options) && e.options[i] != ""
}

func floatingFrame(r Rect) Rect {
	inset := min(float32(floatingLabelInset), r.H/3)
	return Rect{r.X, r.Y + inset, r.W, r.H - inset}
}

func (v *view) showsFieldHint(e *Element) bool {
	return v.focused == e && e.kind == kInput && e.floatingLabel != "" && e.placeholder != ""
}

func (e *Element) emptyFieldText() string {
	if e.floatingLabel != "" {
		return e.floatingLabel
	}
	return e.placeholder
}

// Independently damage the notch when a value crosses the empty boundary.
// For InputGroup the outline belongs to the parent, outside the editor clip.
func (v *view) damageFloatingTitle(e *Element) {
	owner := e
	if e.parent != nil && e.parent.style.tone == groupSurface {
		owner = e.parent
	}
	frame := floatingFrame(owner.rect)
	w := min(e.rect.W, v.inputAdvance(e.floatingLabel, min(float32(12), v.font(e))))
	v.damage(Rect{e.rect.X + 8, frame.Y - floatingLabelInset, w + 8, 2 * floatingLabelInset}.pixels(v.scale))
}

// The caption only needs measuring when a repaint reaches the top outline.
func (v *view) floatingTitleBounds(p *Painter, field *Element, frame Rect) Rect {
	if field.kind == kSelect {
		if !field.hasOptionText(field.selected) {
			return Rect{}
		}
	} else if field.text == "" && !v.showsFieldHint(field) {
		return Rect{}
	}
	x := field.rect.X + 12
	w := field.rect.W - 24
	if field.kind == kSelect {
		w -= 16
	}
	if w <= 0 || !(Rect{x - 4, frame.Y - floatingLabelInset, w + 8, 2 * floatingLabelInset}).pixels(v.scale).Overlaps(p.clip) {
		return Rect{}
	}
	font := min(float32(12), v.font(field))
	h := min(v.inputLineHeight(font), 2*floatingLabelInset)
	return Rect{x, frame.Y - h/2, min(w, v.inputAdvance(field.floatingLabel, font)), h}
}

// Composite fields share the same editor, caret and outline as a standalone input.
func (c *ComboBoxView) FloatingLabel(title string) *ComboBoxView {
	c.Input.FloatingLabel(title)
	return c
}
func (n *NumberField) FloatingLabel(title string) *NumberField {
	n.Input.FloatingLabel(title)
	return n
}
func (d *DateField) FloatingLabel(title string) *DateField { d.Input.FloatingLabel(title); return d }
