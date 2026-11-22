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
	sidebar := Card(navigation).Padding(0).Width(198).FillHeight().Radius(0)
	sidebar.style.tone = sidebarSurface
	return Row(sidebar, content.Grow(1).FillHeight()).Gap(0)
}

// Drawer builds the side panel passed to Document.ShowDrawer.
func Drawer(title string, content *Element, actions ...*Element) *Element {
	return Dialog(title, content, actions...).Width(360).Radius(0)
}

// ShowDrawer opens a modal panel against the right edge of the window.
func (d *Document) ShowDrawer(panel *Element) {
	d.showDialog(panel, true)
}

type Command struct {
	Label    string
	Keywords string
	Action   func()
}

// CommandPalette opens a searchable collection of actions. Enter invokes the
// first match from the query field; Tab moves into the results.
func CommandPalette(doc *Document, title string, commands ...Command) *Element {
	commands = slices.Clone(commands)
	return Button(title, func() {
		query := TextInput("").Label("Search commands").Placeholder("Search commands")
		result := Text("No matching commands").FontSize(13).Hidden(true)
		matches := make([]int, 0, len(commands))
		run := func(i int) {
			cmd := commands[matches[i]]
			doc.CloseDialog()
			cmd.Action()
		}
		name := func(i int) string { return commands[matches[i]].Label }
		options := VirtualListOptions{Name: name}
		list := VirtualList(options, func(i int) *Element {
			return Button(name(i), func() { run(i) }).Height(36)
		}).OnSubmit(run)
		list.Height(216).Label("Commands")
		update := func(text string) {
			matches = matches[:0]
			text = strings.ToLower(strings.TrimSpace(text))
			for i, cmd := range commands {
				if cmd.Action != nil && strings.Contains(strings.ToLower(cmd.Label+" "+cmd.Keywords), text) {
					matches = append(matches, i)
				}
			}
			options.Count = len(matches)
			list.SetOptions(options)
			list.scroll = 0
			list.Hidden(len(matches) == 0)
			result.Hidden(len(matches) != 0)
		}
		query.OnChange(update).OnSubmit(func() {
			if len(matches) != 0 {
				run(0)
			}
		})
		update("")
		doc.ShowDialog(Dialog(title, Column(query, list.Element, result)))
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
	e := TextInput("").Label("Verification code").Placeholder(strings.Repeat("·", length))
	e.own().otpLength = length
	return e.Width(float32(34*length + 20)).OnChange(func(value string) {
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
	width := min(34, e.rect.W/float32(e.otpLength))
	return Rect{e.rect.X + float32(i)*width, e.rect.Y, max(0, width-3), e.rect.H}
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

func Pagination(page, pages int, onChange func(int)) *Element {
	label := Text("")
	var prev, next *Element
	set := func(index int) {
		page = min(max(1, index), max(1, pages))
		label.SetText(fmt.Sprintf("%d / %d", page, max(1, pages)))
		prev.Disabled(page <= 1)
		next.Disabled(page >= pages)
	}
	prev = Button("‹", func() {
		set(page - 1)
		if onChange != nil {
			onChange(page)
		}
	}).Label("Previous page").Width(36)
	next = Button("›", func() {
		set(page + 1)
		if onChange != nil {
			onChange(page)
		}
	}).Label("Next page").Width(36)
	set(page)
	return Row(prev, label, next).Gap(8)
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
		children = append(children, leading)
	}
	children = append(children, input.Grow(1))
	if trailing != nil {
		children = append(children, trailing)
	}
	return Row(children...).Gap(4)
}
