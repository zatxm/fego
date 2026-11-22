package ui

import (
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Sidebar keeps its navigation and content in two retained columns.
func Sidebar(navigation, content *Element) *Element {
	sidebar := Column(navigation).Padding(0).Width(256).FillHeight().Radius(0)
	sidebar.style.tone = sidebarSurface
	return Row(sidebar, content.Grow(1).FillHeight()).Gap(0)
}

// SetSidebarOpen shows or collapses the navigation column of a Sidebar.
func (e *Element) SetSidebarOpen(open bool) *Element {
	if len(e.children) > 0 && e.children[0].style.tone == sidebarSurface {
		e.children[0].Hidden(!open)
	}
	return e
}

// SidebarItem creates a left-aligned navigation control. SetActive highlights the current item.
func SidebarItem(label string, click func()) *Element {
	item := Button(label, click).Height(32).FillWidth()
	item.style.button = sidebarButton
	return item
}

// SetActive sets the current state of a sidebar item.
func (e *Element) SetActive(active bool) *Element {
	if e.style.button == sidebarButton && e.primary != active {
		e.primary = active
		e.touch(false)
	}
	return e
}

// Sheet builds a modal panel for Document.ShowSheet.
func Sheet(title string, content *Element, actions ...*Element) *Element {
	sheet := Dialog(title, content, actions...).Width(384).Radius(0)
	sheet.style.tone = sheetSurface
	sheet.style.card = false
	return sheet
}

// ShowSheet opens a modal panel against the right edge of the window.
func (d *Document) ShowSheet(panel *Element) {
	d.showDialog(panel, rightPanel)
}

// Drawer builds a bottom panel with a drag handle for Document.ShowDrawer.
func Drawer(title string, content *Element, actions ...*Element) *Element {
	drawer := Dialog(title, content, actions...).Padding(34, 24, 24, 24).Radius(14)
	drawer.style.tone = drawerSurface
	drawer.style.card = false
	return drawer
}

// ShowDrawer opens a bottom panel that can be dragged down to dismiss it.
func (d *Document) ShowDrawer(panel *Element) {
	d.showDialog(panel, bottomPanel)
}

type Command struct {
	Label    string
	Keywords string
	Action   func()
}

// CommandPalette opens a searchable collection of actions.
func CommandPalette(doc *Document, title string, commands ...Command) *Element {
	commands = slices.Clone(commands)
	keys := make([]string, len(commands))
	for i, cmd := range commands {
		keys[i] = strings.ToLower(cmd.Label + " " + cmd.Keywords)
	}
	return Button(title, func() {
		query := TextInput("").Label("Search commands").Placeholder("Search commands…").Grow(1)
		result := Text("No matching commands").FontSize(13).Muted().Hidden(true).Padding(16)
		suggestions := Text("Suggestions").FontSize(12).Muted().Padding(8, 12, 4, 12)
		matches := make([]int, 0, len(commands))
		run := func(i int) {
			action := commands[matches[i]].Action
			doc.CloseDialog()
			action()
		}
		list := List(0, func(i int) string { return commands[matches[i]].Label }).Label("Commands").Height(192)
		list.style.tone = commandSurface
		list.OnClick(func() {
			if list.Selected() >= 0 {
				run(list.Selected())
			}
		})
		update := func(text string) {
			matches = matches[:0]
			text = strings.ToLower(strings.TrimSpace(text))
			for i, key := range keys {
				if commands[i].Action != nil && strings.Contains(key, text) {
					matches = append(matches, i)
				}
			}
			list.scroll = 0
			list.SetCount(len(matches)).SetSelected(min(0, len(matches)-1))
			list.Hidden(len(matches) == 0)
			result.Hidden(len(matches) != 0)
			suggestions.Hidden(len(matches) == 0)
		}
		query.OnChange(update).OnSubmit(func() {
			if i := list.Selected(); i >= 0 {
				run(i)
			}
		})
		query.own().onNavigate = func(key string) { list.view.navigate(list, key) }
		search := Canvas(16, 16, func(p *Painter) { p.icon(searchIcon, 8, 8, p.theme.Muted) })
		header := Row(search, query, iconButton(closeIcon, "Close", doc.CloseDialog).Size(24, 24)).Padding(0, 12).Gap(0)
		header.style.tone = commandSurface
		panel := Card(header, Separator(), suggestions, list, result).Label(title).Width(512).Padding(0).Gap(0).Radius(10)
		update("")
		doc.ShowDialog(panel)
		if doc.live != nil {
			doc.live.focus(query)
		}
	})
}

// SearchField pairs a text input with a clear action.
func SearchField(value string, change func(string)) *Element {
	input := TextInput(value).Placeholder("Search").Label("Search").Grow(1)
	clear := Button("×", func() {
		input.SetText("")
		if change != nil {
			change("")
		}
		if input.view != nil {
			input.view.focus(input)
		}
	}).Label("Clear search").Width(36)
	input.OnChange(change)
	return Row(input, clear).Gap(6)
}

// Autocomplete offers matching suggestions while the user types a free-form value.
func Autocomplete(options []string, value string) *ComboBoxView {
	return ComboBox(options, value)
}

// InputOTP accepts a fixed number of ASCII digits, including pasted codes.
func InputOTP(length int, onComplete func(string)) *Element {
	if length < 1 || length > 12 {
		panic("ui: OTP length must be between 1 and 12")
	}
	e := TextInput("").Label("Verification code")
	e.own().otpLength = length
	return e.Width(float32(36 * length)).Height(36).OnChange(func(value string) {
		if len(value) == length && onComplete != nil {
			onComplete(value)
		}
	})
}

func otpDigits(text string, limit int) string {
	var digits [12]byte
	length := 0
	for i := 0; i < len(text) && length < limit; i++ {
		if text[i] >= '0' && text[i] <= '9' {
			digits[length] = text[i]
			length++
		}
	}
	return string(digits[:length])
}

func (e *Element) otpSlot(i int) Rect {
	width := min(36, e.rect.W/float32(e.otpLength))
	return Rect{e.rect.X + float32(i)*width, e.rect.Y, width, e.rect.H}
}

// MultiSelect retains selection by option index. The caller receives a sorted copy.
func MultiSelect(options []string, selected []int, onChange func([]int)) *Element {
	options = slices.Clone(options)
	chosen := make(map[int]bool, len(selected))
	for _, i := range selected {
		if i >= 0 && i < len(options) {
			chosen[i] = true
		}
	}
	count := Text("").FontSize(13)
	list := VirtualList(VirtualListOptions{Count: len(options), Name: func(i int) string { return options[i] }}, func(i int) *Element {
		return Checkbox(options[i], chosen[i]).Padding(0, 10).OnToggle(func(checked bool) {
			if checked {
				chosen[i] = true
			} else {
				delete(chosen, i)
			}
			count.SetText(fmt.Sprintf("%d selected", len(chosen)))
			if onChange != nil {
				keys := make([]int, 0, len(chosen))
				for k := range chosen {
					keys = append(keys, k)
				}
				slices.Sort(keys)
				onChange(keys)
			}
		})
	}).Height(160)
	count.SetText(fmt.Sprintf("%d selected", len(chosen)))
	return Column(count, list).Gap(6)
}

// TokenField edits an array of tags. Enter adds a value and each chip removes it.
func TokenField(values []string, onChange func([]string)) *Element {
	values = slices.Clone(values)
	chips := Column().Gap(4)
	entry := TextInput("").Label("New tag").Placeholder("Add a tag").Grow(1)
	var rebuild func()
	notify := func() {
		rebuild()
		if onChange != nil {
			onChange(slices.Clone(values))
		}
	}
	rebuild = func() {
		buttons := make([]*Element, len(values))
		for i, value := range values {
			buttons[i] = Button(value+"  ×", func() {
				values = slices.Delete(values, i, i+1)
				notify()
			}).Label("Remove " + value).Height(32)
		}
		chips.Children(buttons...)
	}
	add := func() {
		value := strings.TrimSpace(entry.Text())
		if value != "" && !slices.Contains(values, value) {
			values = append(values, value)
			entry.SetText("")
			notify()
		}
	}
	entry.OnSubmit(add)
	rebuild()
	return Column(chips, Row(entry, Button("Add", add)).Gap(6)).Gap(6)
}

// DecimalInput reports exact fixed-point values as strings. Blank and
// intermediate edits stay editable and do not report a new value.
func DecimalInput(value string, places int, onChange func(string)) *Element {
	if places < 0 || places > 18 {
		panic("ui: decimal places must be between 0 and 18")
	}
	return TextInput(value).Label("Decimal").OnChange(func(text string) {
		text = strings.TrimSpace(text)
		s := strings.TrimPrefix(strings.TrimPrefix(text, "-"), "+")
		whole, fraction, _ := strings.Cut(s, ".")
		if len(s) > 128 || len(whole)+len(fraction) == 0 || len(fraction) > places ||
			strings.IndexFunc(whole+fraction, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return
		}
		if exact, ok := new(big.Rat).SetString(text); ok && onChange != nil {
			onChange(exact.FloatString(places))
		}
	})
}

func TimeInput(value time.Time, onChange func(time.Time)) *Element {
	return TextInput(value.Format("15:04")).Label("Time").Placeholder("HH:MM").OnChange(func(text string) {
		if parsed, err := time.Parse("15:04", text); err == nil && onChange != nil {
			onChange(time.Date(value.Year(), value.Month(), value.Day(), parsed.Hour(), parsed.Minute(), 0, 0, value.Location()))
		}
	})
}

// ColorPicker edits an RGB color using its hexadecimal value and preset swatches.
func ColorPicker(value Color, onChange func(Color)) *Element {
	swatch := Card().Padding(0).Size(36, 36).Background(value).Label("Selected color")
	input := TextInput(fmt.Sprintf("#%02X%02X%02X", value.R, value.G, value.B)).Label("Hex color").Grow(1)
	update := func(c Color) {
		swatch.Background(c)
		if onChange != nil {
			onChange(c)
		}
	}
	input.OnChange(func(text string) {
		text = strings.TrimPrefix(strings.TrimSpace(text), "#")
		if len(text) == 6 {
			if n, err := strconv.ParseUint(text, 16, 24); err == nil {
				update(RGB(uint8(n>>16), uint8(n>>8), uint8(n)))
			}
		}
	})
	presets := Grid(6).Gap(4)
	for _, c := range []Color{RGB(24, 24, 27), RGB(250, 250, 250), RGB(239, 68, 68), RGB(245, 158, 11), RGB(34, 197, 94), RGB(59, 130, 246)} {
		label := fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
		presets.Append(Button("", func() { input.SetText(label); update(c) }).Height(28).Background(c).Label(label).Tooltip(label))
	}
	return Column(Row(swatch, input).Gap(6), presets).Gap(6)
}

func SplitButton(label string, action func(), alternatives ...MenuItem) *Element {
	return Row(PrimaryButton(label, action).Grow(1), Menu("▾", alternatives...).Width(36).Label("More "+label+" actions")).Gap(2)
}

func Rating(value, maximum int, onChange func(int)) *Element {
	if maximum < 1 || maximum > 20 {
		panic("ui: rating must have 1 to 20 choices")
	}
	root := Row().Gap(3)
	buttons := make([]*Element, maximum)
	var set func(int)
	for i := range buttons {
		buttons[i] = Button("☆", func() {
			set(i + 1)
			if onChange != nil {
				onChange(i + 1)
			}
		}).Label(fmt.Sprintf("Rate %d of %d", i+1, maximum)).Width(36)
	}
	set = func(value int) {
		for i, b := range buttons {
			b.SetText("☆")
			if i < value {
				b.SetText("★")
			}
		}
	}
	set(value)
	return root.Children(buttons...)
}

// Stepper increments a bounded numeric value using the same input as NumberInput.
func Stepper(value, minimum, maximum, step float64) *NumberField {
	return NumberInput(value, minimum, maximum, step)
}

// InputGroup places accessory controls on either side of an editable field.
func InputGroup(input, leading, trailing *Element) *Element {
	children := make([]*Element, 0, 3)
	if leading != nil {
		styleAddon(leading)
		children = append(children, leading)
	}
	children = append(children, input.Grow(1))
	if trailing != nil {
		styleAddon(trailing)
		children = append(children, trailing)
	}
	group := Row(children...).Gap(0).MinHeight(36)
	group.style.tone = groupSurface
	return group
}

func styleAddon(e *Element) {
	if e.kind == kButton && e.style.button == outlinedButton && !e.primary {
		e.style.button = ghostButton
	}
}
