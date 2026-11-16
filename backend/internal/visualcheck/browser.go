// Package visualcheck renders web pages the way a visitor sees them, at a few
// screen sizes, in a headless Chrome or Edge already installed on the
// computer, and measures what a person would notice: sideways scrolling on a
// phone, text that spills out of its box, unreadable contrast, tap targets
// too small for a finger, broken images and script errors. Mission control's
// critic looks at the screenshots; the measurements keep it honest.
//
// Nothing is installed: the browser is found where its installer puts it,
// started with a throwaway profile under the AO data directory, driven over
// the DevTools protocol on loopback, and closed when the pages are done.
package visualcheck

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrNoBrowser means no Chrome, Edge or Chromium was found on this computer.
var ErrNoBrowser = errors.New("no Chrome, Edge or Chromium browser was found")

// FindBrowser returns the browser to render with: configured when it is set
// (it must exist), else the first Chrome, Edge or Chromium found in the usual
// install locations for this operating system.
func FindBrowser(configured string) (string, error) {
	return finder{
		goos:     runtime.GOOS,
		getenv:   os.Getenv,
		exists:   isFile,
		lookPath: exec.LookPath,
	}.find(configured)
}

type finder struct {
	goos     string
	getenv   func(string) string
	exists   func(string) bool
	lookPath func(string) (string, error)
}

func (f finder) find(configured string) (string, error) {
	if configured = strings.TrimSpace(configured); configured != "" {
		if f.exists(configured) {
			return configured, nil
		}
		return "", fmt.Errorf("the configured browser %q does not exist", configured)
	}
	for _, candidate := range f.candidates() {
		if strings.ContainsAny(candidate, `/\`) {
			if f.exists(candidate) {
				return candidate, nil
			}
			continue
		}
		if found, err := f.lookPath(candidate); err == nil {
			return found, nil
		}
	}
	return "", ErrNoBrowser
}

// candidates lists where Chrome, Edge and Chromium install themselves, most
// common first. Bare names are looked up on PATH.
func (f finder) candidates() []string {
	switch f.goos {
	case "windows":
		var out []string
		for _, root := range []string{f.getenv("ProgramFiles"), f.getenv("ProgramFiles(x86)"), f.getenv("LOCALAPPDATA")} {
			if root == "" {
				continue
			}
			out = append(out,
				filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"),
			)
		}
		return append(out, "chrome.exe", "msedge.exe")
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	default:
		return []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge"}
	}
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
