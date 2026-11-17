package main

import (
	"flag"
	"image/png"
	"log"
	"os"

	"github.com/zatxm/fego"
	"github.com/zatxm/fego/internal/demo"
	"github.com/zatxm/fego/ui"
)

func main() {
	snapshot := flag.String("snapshot", "", "render the same gallery to a PNG without a window")
	dark := flag.Bool("dark", false, "start with the dark theme")
	flag.Parse()
	doc := demo.Build()
	if *dark {
		doc.Theme(ui.Dark)
	}
	if *snapshot != "" {
		t := ui.NewDocumentTester(doc, 1180, 820)
		defer t.Close()
		f, err := os.Create(*snapshot)
		if err != nil {
			log.Fatal(err)
		}
		err = png.Encode(f, t.Snapshot())
		closeErr := f.Close()
		if err != nil {
			log.Fatal(err)
		}
		if closeErr != nil {
			log.Fatal(closeErr)
		}
		return
	}
	if err := fego.Run(fego.WindowOptions{Title: "fego · 工作空间", Width: 1180, Height: 820, Content: doc}); err != nil {
		log.Fatal(err)
	}
}
