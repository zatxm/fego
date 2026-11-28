package ui

import (
	"strings"
	"testing"
)

func TestDialogTextFitsBodyAndKeepsActionsVisible(t *testing.T) {
	doc := View(Box())
	tester := NewDocumentTester(doc, 640, 400)
	defer tester.Close()
	text := Text("short").FillWidth()
	ok := Button("OK", doc.CloseDialog)
	panel := Dialog("Confirm", Column(text), ok).Width(320)
	doc.ShowDialog(panel)
	tester.Step()
	message := strings.Repeat("忘记主机密码？ Keep shared credentials. ", 45) + "\n" + strings.Repeat("0123456789", 20)
	text.SetText(message)
	tester.Step()
	body := panel.children[1]
	if text.Text() != message || !body.scrollable || body.maxScroll() <= 0 || ok.rect.Y+ok.rect.H > panel.rect.Y+panel.rect.H {
		t.Fatal("message, scrolling or actions", body.rect, ok.rect, panel.rect)
	}
	for _, line := range strings.Split(doc.live.labelText(text), "\n") {
		if w, _ := doc.live.text.Measure(line, doc.live.font(text), false); w > text.rect.W+1 {
			t.Fatalf("line overflows: %q", line)
		}
	}
	text.SetText("short again")
	tester.Step()
	if body.scrollable || doc.live.labelText(text) != text.Text() || panel.rect.H >= 368 {
		t.Fatal("shorter message did not reflow")
	}
}
