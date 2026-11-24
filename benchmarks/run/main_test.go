package main

import "testing"

func TestSummaryAndFrameProtocol(t *testing.T) {
	values := []float64{4, 1, 3, 2}
	if got := summarize(values); got != (series{2.5, 1, 4}) || values[0] != 4 {
		t.Fatalf("summary %v, values %v", got, values)
	}
	valid := `done {"frames":10,"wallUs":10000,"p50Us":900,"p99Us":1100,"maxUs":1200}`
	if _, err := decodeFrame(valid, 10); err != nil {
		t.Fatal(err)
	}
	timed := `done {"frames":10,"wallUs":10000,"p50Us":900,"p99Us":1100,"maxUs":1200,"updateP50Us":130,"updateP99Us":210}`
	if f, err := decodeFrame(timed, 10); err != nil || f.UpdateP50US != 130 || f.UpdateP99US != 210 {
		t.Fatalf("update timings: %+v, %v", f, err)
	}
	for _, bad := range []string{`done {}`, valid, `done {"frames":11,"wallUs":10,"p50Us":8,"p99Us":9,"maxUs":12,"updateP50Us":3,"updateP99Us":1}`, `done {"frames":11,"wallUs":10,"p50Us":9,"p99Us":8,"maxUs":12}`} {
		if _, err := decodeFrame(bad, 11); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestMarkerOrdering(t *testing.T) {
	p := &process{lines: make(chan line, 3), pending: make(map[string]line)}
	p.lines <- line{text: "start"}
	p.lines <- line{text: "ready"}
	p.lines <- line{text: `done {"frames":1}`}
	close(p.lines)
	for _, want := range []string{"ready", "start", "done"} {
		if _, err := p.wait(want); err != nil {
			t.Fatalf("lost %s marker: %v", want, err)
		}
	}
}

func TestRendererFallbackMarkerReplacesPreviousInfo(t *testing.T) {
	p := &process{lines: make(chan line, 4), pending: make(map[string]line)}
	p.lines <- line{text: `renderer {"backend":"opengl","vendor":"AMD"}`}
	p.lines <- line{text: "ready"}
	p.lines <- line{text: `renderer {"backend":"cpu","fallback":"context lost"}`}
	p.lines <- line{text: `done {"frames":1}`}
	close(p.lines)
	if _, err := p.wait("ready"); err != nil || p.renderer["backend"] != "opengl" {
		t.Fatal("GPU identity missing", err, p.renderer)
	}
	if _, err := p.wait("done"); err != nil || p.renderer["backend"] != "cpu" || p.renderer["vendor"] != "" {
		t.Fatal("fallback identity stale", err, p.renderer)
	}
}

func TestIdleRendererReportedAfterReady(t *testing.T) {
	p := &process{lines: make(chan line, 1)}
	p.lines <- line{text: `renderer {"backend":"gpu"}`}
	got, err := p.finalRenderer()
	if err != nil || got["backend"] != "gpu" {
		t.Fatal(got, err)
	}
}
