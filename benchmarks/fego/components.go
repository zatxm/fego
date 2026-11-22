package main

import (
	"fmt"
	"image"
	"image/color"
	"log"
	"math"
	"strings"
	"time"

	"github.com/zatxm/fego/v26/ui"
)

var componentScenes = []string{"table", "tree", "form", "editor", "linechart", "linechart-sparse", "barchart", "calendar", "image", "navigation", "richtext"}

func componentScene(name string) (*ui.Element, func(int), int, int) {
	const width, height = 800, 600
	switch name {
	case "table":
		rows := make([][]string, 10000)
		for i := range rows {
			rows[i] = []string{fmt.Sprint(i), rowLabel(i), "Ready"}
		}
		return ui.Table([]ui.ColumnDef{{Title: "ID", Width: 70}, {Title: "Name"}, {Title: "State", Width: 120}}, rows).Fill(), nil, width, height
	case "tree":
		nodes := make([]*ui.TreeNode, 10000)
		for i := range nodes {
			nodes[i] = &ui.TreeNode{Label: rowLabel(i), Children: []*ui.TreeNode{{Label: "Child"}}}
		}
		return ui.Tree(nodes...).Fill(), nil, width, height
	case "form":
		input := ui.TextInput("Workspace")
		number := ui.NumberInput(20, 0, 100, 1)
		check := ui.Checkbox("Enabled", true)
		slider := ui.Slider(20, 0, 100)
		progress := ui.Progress(.2)
		root := ui.Card(ui.Field("Name", input, ""), ui.Row(check, ui.Radio("Mode", true), ui.Switch("Sync", true)), ui.Field("Count", number.Element, ""), ui.Select([]string{"First", "Second"}, 0), ui.ComboBox([]string{"Alpha", "Beta"}, "Alpha").Element, slider, progress, ui.Row(ui.Button("Cancel", nil), ui.PrimaryButton("Save", nil))).Fill()
		return root, func(frame int) {
			input.SetText(fmt.Sprintf("Workspace %d", frame))
			number.SetValue(float64(frame % 100))
			check.SetChecked(frame%2 == 0)
			slider.SetValue(float64(frame % 100))
			progress.SetValue(float64(frame%100) / 100)
		}, width, height
	case "editor":
		return ui.CodeEditor(strings.Repeat("var value = 123 // 中文\n", 30000)).Fill(), nil, width, height
	case "linechart", "linechart-sparse", "barchart":
		count, period := 2048, 23.0
		if name == "linechart-sparse" {
			count, period = 32, 2.3
		}
		series := []ui.Series{{Name: "Alpha", Values: make([]float64, count)}, {Name: "Beta", Values: make([]float64, count)}}
		chart := ui.LineChart()
		if name == "barchart" {
			chart = ui.BarChart()
		}
		update := func(frame int) {
			for i := range series {
				for j := range series[i].Values {
					series[i].Values[j] = math.Sin(float64(j+frame)/period+float64(i)) * 70
				}
			}
			chart.SetSeries(series...)
		}
		update(0)
		return chart.Element.Fill(), update, width, height
	case "calendar":
		date := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
		calendar := ui.Calendar(date)
		return ui.Column(calendar.Element).Center(), func(frame int) { calendar.SetDate(date.AddDate(0, 0, frame%28)) }, width, height
	case "image":
		img := image.NewRGBA(image.Rect(3, 7, 259, 199))
		for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
			for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
				img.SetRGBA(x, y, color.RGBA{uint8(x), uint8(y), uint8(x + y), 255})
			}
		}
		root := ui.Image(img).Fill()
		return root, func(int) { root.Refresh() }, width, height
	case "navigation":
		tabs := ui.Tabs(ui.Tab{Title: "Form", Content: ui.Form(ui.Field("Name", ui.TextInput("Retained"), ""), ui.Field("Choice", ui.Select([]string{"One", "Two"}, 0), ""))}, ui.Tab{Title: "Sections", Content: ui.Accordion(ui.Section{Title: "Details", Content: ui.TextArea("Text"), Open: true})}, ui.Tab{Title: "Actions", Content: ui.Column(ui.Breadcrumbs(ui.Breadcrumb{Label: "Home"}, ui.Breadcrumb{Label: "Page"}), ui.Menu("Actions", ui.MenuItem{Label: "Save"}), ui.Segmented([]string{"Day", "Month"}, 0).Element, ui.Badge("Ready"), ui.Avatar("Native UI", 48))})
		return tabs.Element.Fill(), func(frame int) { tabs.SetSelected(frame % 3) }, width, height
	case "richtext":
		return ui.ScrollView(ui.Markdown(strings.Repeat("## Section\nText with **bold**, `code`, and [link](https://example.com).\n- Item\n", 100), nil)).Fill(), nil, width, height
	default:
		log.Fatalf("unknown scene %q", name)
		return nil, nil, 0, 0
	}
}
