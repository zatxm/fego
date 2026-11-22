package main

import (
	"fmt"
	"log"

	"github.com/zatxm/fego/v26"
	"github.com/zatxm/fego/v26/ui"
)

func main() {
	var doc *ui.Document
	count := 0
	value := ui.Text("0").FontSize(25).Bold()
	increment := ui.PrimaryButton("Increment", func() {
		count++
		value.SetText(fmt.Sprint(count))
	})
	verification := ui.Text("").FontSize(13)
	code := ui.InputOTP(4, func(string) { verification.SetText("Code accepted") })
	list := ui.VirtualList(ui.VirtualListOptions{Count: 100000}, func(i int) *ui.Element {
		title := ui.Text(fmt.Sprintf("Item %06d", i)).Grow(1)
		if i%3 == 0 {
			title.Height(44)
		}
		return ui.Row(title, ui.Button("Open", func() {
			doc.ShowToast(fmt.Sprintf("Opened item %d", i))
		}).Height(36)).Padding(4, 10).Gap(8)
	})
	list.Label("Items").Height(280)
	dark := ui.Switch("Dark appearance", false).OnToggle(func(on bool) {
		theme := ui.Light
		if on {
			theme = ui.Dark
		}
		doc.Theme(theme)
	})
	doc = ui.View(ui.ScrollView(ui.Column(
		ui.Text("fego").FontSize(27).Bold(),
		ui.Card(ui.Row(value.Grow(1), increment), dark),
		ui.Card(ui.Text("Verification code").Bold(), code, verification),
		ui.Text("Items").Bold(), list.Element,
	).Padding(16)).Fill())
	if err := fego.Run(fego.WindowOptions{Title: "fego", Content: doc}); err != nil {
		log.Fatal(err)
	}
}
