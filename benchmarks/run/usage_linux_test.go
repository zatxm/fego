package main

import (
	"os"
	"testing"
)

func TestProcParsingAndLiveUsage(t *testing.T) {
	stat := "123 (name with ) parentheses) R 1 2 3 4 5 6 7 8 9 10 123 45 0"
	if got, err := parseTicks(stat); err != nil || got != 168 {
		t.Fatalf("ticks %d: %v", got, err)
	}
	for _, bad := range []string{"", "1 (x) R", "1 (x) R 1 2 3 4 5 6 7 8 9 10 x 5"} {
		if _, err := parseTicks(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if got, err := fieldKB("Pss: 123 kB\nPss_Dirty: 12 kB", "Pss"); err != nil || got != 123*1024 {
		t.Fatalf("Pss %d: %v", got, err)
	}
	for _, bad := range []string{"Pss_Dirty: 12 kB", "Pss: 12 bytes", "Pss:"} {
		if _, err := fieldKB(bad, "Pss"); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	u, err := usageOf(os.Getpid())
	if err != nil || u.rss == 0 || u.pss == 0 || u.peak == 0 {
		t.Fatalf("live usage %+v: %v", u, err)
	}
}
