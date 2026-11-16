// Package clipboard copies text to the system clipboard with the platform's
// own tools.
package clipboard

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Copy puts text on the clipboard.
func Copy(text string) error {
	for _, c := range commands() {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		cmd := exec.CommandContext(ctx, c[0], c[1:]...) //nolint:gosec // a fixed list of clipboard tools
		cmd.Stdin = strings.NewReader(text)
		err := cmd.Run()
		cancel()
		if err == nil {
			return nil
		}
	}
	return errors.New("no clipboard tool found (pbcopy, wl-copy, xclip, xsel or clip)")
}

func commands() [][]string {
	switch runtime.GOOS {
	case "darwin":
		return [][]string{{"pbcopy"}}
	case "windows":
		return [][]string{{"clip"}}
	default:
		var out [][]string
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			out = append(out, []string{"wl-copy"})
		}
		return append(out, []string{"xclip", "-selection", "clipboard"}, []string{"xsel", "--clipboard", "--input"}, []string{"wl-copy"})
	}
}
