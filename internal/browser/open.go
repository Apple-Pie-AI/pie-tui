// Package browser opens a file or URL with the OS's default handler.
package browser

import (
	"os/exec"
	"runtime"
)

// Open hands target to `open` (macOS) or `xdg-open` and does not wait.
func Open(target string) error {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	return exec.Command(opener, target).Start()
}
