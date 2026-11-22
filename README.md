# fego

**English** | [简体中文](README.zh-CN.md)

A lightweight Go toolkit for native desktop, mobile, and browser UI. Linux, Windows, and macOS desktop builds use pure Go. Android and iOS use their native SDKs.

Build layouts, forms, data views, and text editors with retained Go controls and light and dark themes using the [shadcn Neutral color tokens](https://ui.shadcn.com/docs/theming#default-theme-css).

[Example](examples/gallery/main.go) · [Performance data](benchmarks/README.md) · [Changelog](CHANGELOG.md) · [CI](https://github.com/zatxm/fego/actions/workflows/ci.yml)

| Light controls | Dark controls |
| --- | --- |
| ![Light controls](docs/images/controls-light.png) | ![Dark controls](docs/images/controls-dark.png) |

## Supported platforms

| Platform | Window system | Architectures |
| --- | --- | --- |
| Linux | X11; XWayland on Wayland desktops | amd64, arm64 |
| Windows | Win32 | amd64, arm64 |
| macOS | Cocoa | amd64, arm64 |
| Android | Native activity | arm64, amd64 |
| iOS | UIKit | arm64 |
| Browser | Canvas | WebAssembly |

Linux requires X11 or XWayland at runtime; when `libXext` and a local server provide MIT-SHM, window updates use shared memory and otherwise fall back to regular uploads. CJK text needs system fonts with the appropriate glyphs; Noto Sans CJK is recommended on Linux. Windows and macOS use system fonts. An embedded Go font is used when no suitable system Latin font is available.

## Quick start

Install Go 1.27.4 or newer. In an empty directory, create a project and add the dependency:

```sh
go mod init example.com/hello-fego
go get github.com/zatxm/fego/v26
```

Create `main.go`:

```go
package main

import (
	"log"
	"strconv"

	"github.com/zatxm/fego/v26"
	"github.com/zatxm/fego/v26/ui"
)

func main() {
	count := 0
	label := ui.Text("0").FontSize(40).Bold()
	button := ui.PrimaryButton("Increment", func() {
		count++
		label.SetText(strconv.Itoa(count))
	})
	doc := ui.View(ui.Column(label, button).Center().Padding(24))
	if err := fego.Run(fego.WindowOptions{
		Title: "Hello fego", Width: 400, Height: 300, Content: doc,
	}); err != nil {
		log.Fatal(err)
	}
}
```

Run the application or build an executable:

```sh
go run .
go build .
```

For Android and iOS build instructions, see [Mobile](docs/mobile.md).

## Run the gallery

Clone the repository to explore forms, tables, dialogs, page navigation, and theme switching:

```sh
git clone https://github.com/zatxm/fego.git
cd fego
go run ./examples/gallery
```

Use command-line options to start with a dark theme or save a window screenshot:

```sh
go run ./examples/gallery -dark
go run ./examples/gallery -snapshot gallery.png
```

## Run in a browser

Build the same Go controls as WebAssembly and serve them over HTTP.

```sh
mkdir -p /tmp/fego-web
cp examples/web/index.html examples/web/main.js /tmp/fego-web/
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" /tmp/fego-web/
GOOS=js GOARCH=wasm go build -o /tmp/fego-web/app.wasm ./examples/studio
python3 -m http.server 8080 --directory /tmp/fego-web
```

Open `http://localhost:8080`. The app mounts into the element with ID `fego`. Give that element a height and add `data-fego-fill` to have windows follow its dimensions. Serve `wasm_exec.js` from the same Go toolchain used to build the app. The demo accepts `?dark` for the dark theme and `?video=sample.webm` if you place that file next to `app.wasm`.

## System tray and multiple windows

Run `go run ./examples/tray` inside a desktop session with tray support. The example starts in the tray, opens a window from its menu and keeps running after that window closes. Choose **Quit** from the tray to exit.

Set `AppOptions.ID`, `Name` and `Icon` before `Run`. On Linux, match the ID to your desktop entry's `StartupWMClass`. On Windows, use the same AppUserModelID for packaged shortcuts. On macOS, set the bundle identifier in the app's Info.plist. The bundle and installer also provide the icon shown by the file manager.

Use `Window.Show`, `Hide` and `Activate` to control a specific window. Register `Window.OnClose` to intercept a close request. `AppOptions.KeepAlive` allows apps to run with no windows and `app.Quit()` exits the whole app.

Create trays and update their menus on the UI thread. From worker goroutines, use `app.Post`. Linux needs a StatusNotifierItem host on the session bus. `Tray.Available()` reports whether a host is available. Primary click and tooltip presentation depend on the desktop. The macOS menu bar opens an attached native menu on click.

## Widgets and features

| Category | APIs |
| --- | --- |
| Layout | Box, Row, Column, Grid, Card, ScrollView, spacing, padding, sizing, and alignment |
| Basic widgets | Text, RichText, Button, PrimaryButton, Checkbox, Radio, Switch, Separator, Image |
| Forms | TextInput, TextArea, password fields, Field, Form, Select, ComboBox, Autocomplete, SearchField, MultiSelect, TokenField, InputGroup, InputOTP, NumberInput, Stepper, DecimalInput, TimeInput, ColorPicker, Slider, RangeSlider, Rating, Progress |
| Navigation and feedback | Tabs, Segmented, Accordion, Breadcrumbs, Sidebar, CommandPalette, Pagination, Menu, Dropdown, SplitButton, Dialog, Drawer, Toast, Badge, Avatar |
| Data | List, VirtualList, Table with column sorting, Tree with expand/collapse, LineChart, BarChart, AreaChart, StackedBarChart, PieChart |
| Dates and documents | Calendar, DatePicker, CodeEditor, Editor, MarkdownEditor, Diff, Merge |
| Drawing and media | Canvas, Painter, Path, EaseInOut, Spring, Keyframes, Timeline, Video, optional GPU presentation and GLSL shaders |
| Input | Mouse, scrolling, Tab and arrow keys, Unicode, selection, clipboard, undo, system input method text commits |
| Accessibility | Linux AT-SPI, Windows MSAA, macOS NSAccessibility, browser ARIA |

## Updating state and layout

Create the widget tree once, then update existing widgets with methods such as `SetText`, `SetValue`, and `SetSelected`. Each widget belongs to one parent or window. Calling a setter does not invoke user interaction callbacks.

Event callbacks run on the UI thread. When a background goroutine receives data, use `window.Update(func() { ... })` or `app.Post` to update the interface. Call `fego.Run` from `main`. When managing windows with `NewApp`, configure `WhenReady` before starting the application and create windows from that callback.

Layout dimensions use logical pixels. `FillWidth` and `FillHeight` fill their respective axes; `Grow` distributes remaining space along the parent container's main axis. Use `ScrollView` for content larger than the visible area. Keyboard focus automatically scrolls the target widget into view.

Composite widgets such as `Tabs`, `DatePicker`, `NumberInput`, `ComboBox`, and charts expose `.Element` for placement in layouts. Set dimensions with methods such as `.Element.Width(...)`, and keep the original object to update values through its dedicated methods. `Badge` and `Avatar` are static decorations; replace the node to change their content.

Lists, tables, and trees draw only visible rows. Call `Refresh` after changing list contents, and `SetCount` when its row count changes. Use `SetRows` to update table data. These widgets reuse scroll regions where possible to reduce repainting. On Linux the X server retains window pixels, so fego paints only damaged regions through a bounded scratch band instead of keeping a full-window client image.

## Lists with custom rows

Use `ui.List` for fixed-height text rows and `ui.VirtualList` when a row needs its own layout or controls:

```go
list := ui.VirtualList(ui.VirtualListOptions{Count: len(items), Name: func(i int) string {
    return items[i].Name
}}, func(i int) *ui.Element {
    return ui.Row(ui.Text(items[i].Name).Grow(1), ui.Button("Open", func() { open(items[i]) }))
})
list.Fill()
```

The list mounts visible rows and a small buffer around them. Keep durable state in your data model. Use `Key` for stable selection through reordering, `Headers` for pinned sections, and `Reorder` to apply a drag to your data. Shift extends selection and Ctrl or Command toggles rows in multi-select mode. Use `Refresh` to rebuild rows after editing their data.

## Documents, drawing, and video

Run `go run ./examples/studio` for an editor, a three-way merge, animated drawing, charts, and the GPU switch. Use `go run ./examples/studio -video path/to/file.webm` to open the video tab. Desktop playback reads a local file through FFmpeg on PATH and plays the video track. Browser playback uses the browser decoder and supports audio, with a same-origin or CORS-enabled media URL. Call `Play` during a button or other user gesture on the web. Video playback is available on desktop and in the browser.

Use `ui.Editor` for find and replace, fold ranges, and multiple carets. Alt-click adds a caret. Use the gutter chevron to collapse or expand an indented block. Use `ui.MarkdownEditor` to preview changes. `ui.Diff` displays a side-by-side comparison. `ui.Merge` applies independent changes and lets users choose a resolution for each overlap. Its `Text` method reports whether all conflicts are resolved.

Use `ui.Path` with `FillPath` or `StrokePath` in a canvas. Charts use the shadcn Neutral palette and show values on hover or with keyboard selection. `ui.Animate` and `ui.Timeline` run on the UI thread and schedule frames while tracks are active.

GPU presentation defaults to off. Set `WindowOptions.GPU` at creation or call `window.SetGPUEnabled(true)` on the UI thread to enable it. Linux, Windows, and macOS use OpenGL, and the browser uses WebGL 2. `window.GPUActive()` reports whether initialization succeeded. CPU presentation resumes when the hardware path is unavailable. Controls and paths are rasterized in Go. Applications can apply a full-frame GLSL effect with `window.SetFragmentShader` using `frameTexture` and `frameUV`. The mobile platforms use the system compositor.

## Text and accessibility

CJK text relies on system fonts for the required glyphs. Text editing offsets use Unicode runes. Full grapheme-cluster editing, complex text shaping, BiDi, and color emoji are not yet supported.

Password fields disable copying and cutting, and accessibility interfaces expose only masked text. On Linux, enable the system accessibility service before starting the application. If you enable a screen reader after the application has started, restart the application.

## Limitations

- Linux windows use X11 or XWayland. The canvas supports two-dimensional drawing.
- `CodeEditor` provides text editing and line numbers, without LSP or rich-text WYSIWYG editing.
- Markdown supports common syntax but does not implement all of CommonMark. HTML is displayed as plain text. Links notify the application through a callback and accept only http, https, and mailto.

See [Performance data](benchmarks/README.md) for measurement methods, scenarios, and raw results.
