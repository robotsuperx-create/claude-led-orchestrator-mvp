package visualcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFindBrowserPrefersTheConfiguredOneThenInstallLocations(t *testing.T) {
	present := map[string]bool{}
	f := finder{
		goos: "windows",
		getenv: func(key string) string {
			return map[string]string{"ProgramFiles": "/pf", "LOCALAPPDATA": "/local"}[key]
		},
		exists:   func(path string) bool { return present[path] },
		lookPath: func(string) (string, error) { return "", errors.New("not on PATH") },
	}
	if _, err := f.find(""); !errors.Is(err, ErrNoBrowser) {
		t.Fatalf("find with nothing installed = %v, want ErrNoBrowser", err)
	}
	edge := filepath.Join("/pf", "Microsoft", "Edge", "Application", "msedge.exe")
	present[edge] = true
	if got, err := f.find(""); err != nil || got != edge {
		t.Fatalf("find = %q, %v; want Edge", got, err)
	}
	chrome := filepath.Join("/local", "Google", "Chrome", "Application", "chrome.exe")
	present[chrome] = true
	if got, _ := f.find(""); got != edge {
		t.Fatalf("find = %q; Program Files comes before the user's own install", got)
	}
	if got, err := f.find(chrome); err != nil || got != chrome {
		t.Fatalf("find(configured) = %q, %v", got, err)
	}
	if _, err := f.find("/nowhere/chrome.exe"); err == nil {
		t.Fatal("a configured browser that does not exist was accepted")
	}
}

func TestFindBrowserLooksUpBareNamesOnLinux(t *testing.T) {
	f := finder{
		goos:   "linux",
		getenv: func(string) string { return "" },
		exists: func(string) bool { return false },
		lookPath: func(name string) (string, error) {
			if name == "chromium" {
				return "/usr/bin/chromium", nil
			}
			return "", errors.New("missing")
		},
	}
	if got, err := f.find(""); err != nil || got != "/usr/bin/chromium" {
		t.Fatalf("find = %q, %v", got, err)
	}
}

func TestReadDevToolsPort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "DevToolsActivePort")
	for content, want := range map[string]string{
		"9222\n/devtools/browser/abc\n":  "ws://127.0.0.1:9222/devtools/browser/abc",
		"9222\r\n/devtools/browser/abc":  "ws://127.0.0.1:9222/devtools/browser/abc",
		"9222\n":                         "",
		"x\n/devtools/browser/abc\n":     "",
		"9222\n/elsewhere\n":             "",
		"70000\n/devtools/browser/abc\n": "",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		got, ok := readDevToolsPort(path)
		if got != want || ok != (want != "") {
			t.Errorf("readDevToolsPort(%q) = %q, %v; want %q", content, got, ok, want)
		}
	}
}

func TestFileURL(t *testing.T) {
	if got := fileURL("/tmp/site/index.html"); got != "file:///tmp/site/index.html" {
		t.Fatalf("fileURL = %q", got)
	}
	if got := fileURL("/tmp/my site/index.html"); got != "file:///tmp/my%20site/index.html" {
		t.Fatalf("fileURL with a space = %q", got)
	}
}

func TestFindPages(t *testing.T) {
	root := t.TempDir()
	write := func(name string) {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("<h1>x</h1>"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if pages := FindPages(root, nil); len(pages) != 0 {
		t.Fatalf("a repository without pages yields %v", pages)
	}
	write("b.html")
	write("a.html")
	write("notes.md")
	labels := func(pages []Page) string {
		var out []string
		for _, page := range pages {
			out = append(out, page.Label)
		}
		return strings.Join(out, ",")
	}
	if got := labels(FindPages(root, nil)); got != "a.html,b.html" {
		t.Fatalf("root pages = %q", got)
	}
	write("public/index.html")
	if got := labels(FindPages(root, nil)); got != "public/index.html" {
		t.Fatalf("a site's front page = %q, want public/index.html", got)
	}
	write("index.html")
	if got := labels(FindPages(root, nil)); got != "index.html" {
		t.Fatalf("front page = %q, want index.html", got)
	}
	if got := labels(FindPages(root, []string{"b.html", "../outside.html", "/etc/passwd", "notes.md", "missing.html", "b.html"})); got != "b.html" {
		t.Fatalf("configured pages = %q, want only b.html", got)
	}
}

func TestErrorLogKeepsDistinctErrorsUpToALimit(t *testing.T) {
	log := newErrorLog()
	event := func(method string, params any) cdpMessage {
		data, _ := json.Marshal(params)
		return cdpMessage{Method: method, Params: data}
	}
	log.note(event("Runtime.exceptionThrown", map[string]any{"exceptionDetails": map[string]any{"exception": map[string]any{"description": "TypeError: x is null"}}}))
	log.note(event("Runtime.exceptionThrown", map[string]any{"exceptionDetails": map[string]any{"exception": map[string]any{"description": "TypeError: x is null"}}}))
	log.note(event("Runtime.consoleAPICalled", map[string]any{"type": "log", "args": []any{map[string]any{"value": "hello"}}}))
	log.note(event("Runtime.consoleAPICalled", map[string]any{"type": "error", "args": []any{map[string]any{"value": "boom"}}}))
	log.note(event("Log.entryAdded", map[string]any{"entry": map[string]any{"level": "error", "text": "Failed to load resource", "url": "file:///x/logo.png"}}))
	log.note(event("Log.entryAdded", map[string]any{"entry": map[string]any{"level": "warning", "text": "meh"}}))
	got := strings.Join(log.findings(), " | ")
	want := "Browser error: TypeError: x is null | Browser error: boom | Browser error: Failed to load resource (file:///x/logo.png)"
	if got != want {
		t.Fatalf("findings = %q\nwant      %q", got, want)
	}
	for i := 0; i < 10; i++ {
		log.note(event("Runtime.consoleAPICalled", map[string]any{"type": "error", "args": []any{map[string]any{"value": i}}}))
	}
	if n := len(log.findings()); n != maxErrorsReported {
		t.Fatalf("kept %d errors, want at most %d", n, maxErrorsReported)
	}
}

// TestCaptureInARealBrowser renders a page with known problems in the Chrome,
// Edge or Chromium on this machine. It runs only when AO_VISUAL_E2E is set,
// because a browser is not part of every build machine.
func TestCaptureInARealBrowser(t *testing.T) {
	if os.Getenv("AO_VISUAL_E2E") == "" {
		t.Skip("set AO_VISUAL_E2E=1 to render in a real browser")
	}
	browser, err := FindBrowser(os.Getenv("AO_VISUAL_BROWSER"))
	if err != nil {
		t.Skipf("no browser: %v", err)
	}
	root := t.TempDir()
	page := filepath.Join(root, "index.html")
	html := `<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"></head>
<body style="margin:0;background:#fff">
<h2 style="color:#ddd">Faint heading</h2>
<div style="width:1200px;height:40px;background:#c00">too wide on a phone</div>
<img src="missing.png" alt="gone">
<a href="#" style="font-size:10px">tiny</a>
<script>console.error("boom from the page")</script>
</body></html>`
	if err := os.WriteFile(page, []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	shots, err := Capture(ctx, Options{Browser: browser, ProfileDir: filepath.Join(t.TempDir(), "profiles"), Settle: 300 * time.Millisecond}, []Page{{Label: "index.html", File: page}})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(shots) != len(DefaultViewports) {
		t.Fatalf("got %d shots, want one per viewport", len(shots))
	}
	phone := shots[len(shots)-1]
	if phone.Viewport.Name != "phone" || !bytes.HasPrefix(phone.JPEG, []byte{0xFF, 0xD8}) {
		t.Fatalf("phone shot = %s, %d bytes", phone.Viewport.Name, len(phone.JPEG))
	}
	all := strings.Join(phone.Findings, "\n")
	for _, want := range []string{"scrolls sideways", "does not load", "no h1", "Low contrast", "Tap target too small", "boom from the page"} {
		if !strings.Contains(all, want) {
			t.Errorf("phone findings miss %q:\n%s", want, all)
		}
	}
	if strings.Contains(strings.Join(shots[0].Findings, "\n"), "scrolls sideways") {
		t.Errorf("the 1440px desktop shot should not scroll sideways: %v", shots[0].Findings)
	}
}
