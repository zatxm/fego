//go:build linux && !android

package text

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

var desktopFont = sync.OnceValues(func() ([2]candidate, [2]bool) {
	family := "sans-serif"
	desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP"))
	switch {
	case strings.Contains(desktop, "kde"):
		for _, program := range []string{"kreadconfig6", "kreadconfig5"} {
			value, err := exec.Command(program, "--file", "kdeglobals", "--group", "General", "--key", "font").Output()
			if name, _, _ := strings.Cut(strings.TrimSpace(string(value)), ","); err == nil && name != "" {
				family = name
				break
			}
		}
	default:
		if value, err := exec.Command("gsettings", "get", "org.gnome.desktop.interface", "font-name").Output(); err == nil {
			name := strings.Trim(strings.TrimSpace(string(value)), "'\"")
			if base := strings.TrimRight(name, "0123456789. "); base != "" {
				family = base
			}
		}
	}
	var fonts [2]candidate
	var ok [2]bool
	for weight, style := range []string{"Regular", "Bold"} {
		value, err := exec.Command("fc-match", "-f", "%{file}\t%{index}", family+":style="+style).Output()
		file, number, found := strings.Cut(string(value), "\t")
		index, parseErr := strconv.Atoi(number)
		if err == nil && found && file != "" && parseErr == nil && index >= 0 {
			fonts[weight], ok[weight] = candidate{file, index & 0xffff}, true
		}
	}
	return fonts, ok
})

func init() {
	systemLatin = func(s *System, bold bool) *source {
		fonts, ok := desktopFont()
		weight := 0
		if bold {
			weight = 1
			// sfnt cannot select a named instance of a variable font.
			// Keep the existing bold face if Fontconfig resolves both
			// weights to the same underlying face.
			if fonts[0] == fonts[1] {
				return nil
			}
		}
		if !ok[weight] {
			return nil
		}
		return s.openCandidates(fonts[weight : weight+1])
	}
}
