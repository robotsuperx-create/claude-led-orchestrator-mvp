package visualcheck

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

//go:embed audit.js
var auditScript string

// Viewport is one screen size a page is judged at.
type Viewport struct {
	Name   string
	Width  int
	Height int
	Mobile bool
}

// DefaultViewports are a laptop, a tablet and a phone.
var DefaultViewports = []Viewport{
	{Name: "desktop", Width: 1440, Height: 900},
	{Name: "tablet", Width: 768, Height: 1024},
	{Name: "phone", Width: 390, Height: 844, Mobile: true},
}

// Page is one HTML file to render: Label names it for people and the critic,
// File is its absolute path on disk.
type Page struct {
	Label string
	File  string
}

// Shot is one page at one size: a JPEG of the whole page (up to the height
// limit) and what the measurements found there.
type Shot struct {
	Page     string
	Viewport Viewport
	JPEG     []byte
	// Height is the page's full height in CSS pixels; the image may stop
	// short of it at the height limit.
	Height   int
	Findings []string
	// Tiles is the same picture cut top to bottom into pieces small enough
	// for a model to read at full detail; a short page is one tile.
	Tiles [][]byte
}

// Options configures one capture. Browser is required; the rest has defaults.
type Options struct {
	Browser   string
	Viewports []Viewport
	// ProfileDir is where the throwaway browser profile is created and
	// removed again; it should be under the AO data directory.
	ProfileDir string
	// Settle is how long a page may animate after it has loaded.
	Settle time.Duration
	// MaxHeight bounds how much of a long page is photographed.
	MaxHeight int
	// PageTimeout bounds loading and measuring one page at one size.
	PageTimeout time.Duration
}

const (
	defaultSettle      = 1500 * time.Millisecond
	defaultMaxHeight   = 6000
	defaultPageTimeout = 45 * time.Second
	startTimeout       = 20 * time.Second
	jpegQuality        = 80
	// A model scales a picture down past about 1.15 megapixels or a 1568px
	// edge, which blurs a tall page's text. Tiles keep within a little more
	// than that, and a page is cut into at most maxTiles of them.
	tilePixels = 1_600_000
	tileEdge   = 1568
	maxTiles   = 6
)

// tileHeight is how tall one tile of a page this wide may be.
func tileHeight(width int) int {
	if width <= 0 {
		return tileEdge
	}
	return max(min(tilePixels/width, tileEdge), 200)
}

// tileSpans cuts a captured height into tiles: [top, height] pairs. A last
// sliver shorter than a fifth of a tile is folded into the tile before it.
func tileSpans(captured, width int) [][2]int {
	step := tileHeight(width)
	if captured <= step+step/5 {
		return [][2]int{{0, captured}}
	}
	var spans [][2]int
	for top := 0; top < captured && len(spans) < maxTiles; top += step {
		spans = append(spans, [2]int{top, min(step, captured-top)})
	}
	if last := len(spans) - 1; last > 0 && spans[last][1] < step/5 {
		spans[last-1][1] += spans[last][1]
		spans = spans[:last]
	}
	return spans
}

// Capture renders every page at every viewport in one headless browser.
func Capture(ctx context.Context, options Options, pages []Page) ([]Shot, error) {
	if strings.TrimSpace(options.Browser) == "" {
		return nil, ErrNoBrowser
	}
	if len(pages) == 0 {
		return nil, nil
	}
	if len(options.Viewports) == 0 {
		options.Viewports = DefaultViewports
	}
	if options.Settle <= 0 {
		options.Settle = defaultSettle
	}
	if options.MaxHeight <= 0 {
		options.MaxHeight = defaultMaxHeight
	}
	if options.PageTimeout <= 0 {
		options.PageTimeout = defaultPageTimeout
	}
	if options.ProfileDir != "" {
		if err := os.MkdirAll(options.ProfileDir, 0o700); err != nil {
			return nil, fmt.Errorf("prepare the browser profile: %w", err)
		}
	}
	profile, err := os.MkdirTemp(options.ProfileDir, "visual-")
	if err != nil {
		return nil, fmt.Errorf("prepare the browser profile: %w", err)
	}
	defer func() { _ = os.RemoveAll(profile) }()

	// The browser outlives a canceled capture only until stop runs.
	browserCtx, cancelBrowser := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelBrowser()
	command := aoprocess.CommandContext(browserCtx, options.Browser, browserArgs(profile)...)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start the browser: %w", err)
	}
	exited := make(chan struct{})
	go func() { _ = command.Wait(); close(exited) }()
	stop := func() {
		cancelBrowser()
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
		}
	}
	defer stop()

	endpoint, err := waitForDevTools(ctx, profile, exited)
	if err != nil {
		return nil, err
	}
	client, err := dialCDP(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	defer client.close()

	var shots []Shot
	for _, page := range pages {
		for _, viewport := range options.Viewports {
			if err := ctx.Err(); err != nil {
				return shots, err
			}
			shot, err := capturePage(ctx, client, options, page, viewport)
			if err != nil {
				return shots, fmt.Errorf("render %s at %s: %w", page.Label, viewport.Name, err)
			}
			shots = append(shots, shot)
		}
	}
	_ = client.call(ctx, "", "Browser.close", nil, nil)
	return shots, nil
}

// browserArgs starts a headless browser with a fresh profile, DevTools on a
// loopback port it picks itself, and nothing that phones home or syncs.
func browserArgs(profile string) []string {
	args := []string{
		"--headless=new",
		"--remote-debugging-port=0",
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-extensions",
		"--disable-background-networking",
		"--disable-sync",
		"--disable-default-apps",
		"--hide-scrollbars",
		"--mute-audio",
		"--force-color-profile=srgb",
	}
	// Chrome refuses to start its sandbox as root on Linux (containers, CI).
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		args = append(args, "--no-sandbox")
	}
	return append(args, "about:blank")
}

// waitForDevTools reads the port and path the browser writes once its
// DevTools endpoint is listening.
func waitForDevTools(ctx context.Context, profile string, exited <-chan struct{}) (string, error) {
	deadline := time.NewTimer(startTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		if endpoint, ok := readDevToolsPort(filepath.Join(profile, "DevToolsActivePort")); ok {
			return endpoint, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-exited:
			return "", errors.New("the browser exited before it was ready")
		case <-deadline.C:
			return "", errors.New("the browser did not start in time")
		case <-tick.C:
		}
	}
}

func readDevToolsPort(path string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	var lines []string
	for scanner.Scan() && len(lines) < 2 {
		lines = append(lines, strings.TrimSpace(scanner.Text()))
	}
	if len(lines) < 2 {
		return "", false
	}
	port, err := strconv.Atoi(lines[0])
	if err != nil || port <= 0 || port > 65535 || !strings.HasPrefix(lines[1], "/devtools/browser/") {
		return "", false
	}
	return "ws://127.0.0.1:" + lines[0] + lines[1], true
}

func capturePage(parent context.Context, client *cdpClient, options Options, page Page, viewport Viewport) (Shot, error) {
	ctx, cancel := context.WithTimeout(parent, options.PageTimeout)
	defer cancel()
	var target struct {
		TargetID string `json:"targetId"`
	}
	if err := client.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, &target); err != nil {
		return Shot{}, err
	}
	defer func() {
		_ = client.call(context.WithoutCancel(ctx), "", "Target.closeTarget", map[string]any{"targetId": target.TargetID}, nil)
	}()
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := client.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, &attached); err != nil {
		return Shot{}, err
	}
	session := attached.SessionID
	for _, method := range []string{"Page.enable", "Runtime.enable", "Log.enable"} {
		if err := client.call(ctx, session, method, nil, nil); err != nil {
			return Shot{}, err
		}
	}
	if err := client.call(ctx, session, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": viewport.Width, "height": viewport.Height, "deviceScaleFactor": 1, "mobile": viewport.Mobile,
	}, nil); err != nil {
		return Shot{}, err
	}
	if viewport.Mobile {
		_ = client.call(ctx, session, "Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 5}, nil)
	}

	client.drain()
	errorsSeen := newErrorLog()
	var navigation struct {
		ErrorText string `json:"errorText"`
	}
	if err := client.call(ctx, session, "Page.navigate", map[string]any{"url": fileURL(page.File)}, &navigation); err != nil {
		return Shot{}, err
	}
	if navigation.ErrorText != "" {
		return Shot{}, fmt.Errorf("the page did not open: %s", navigation.ErrorText)
	}
	if err := waitForEvent(ctx, client, session, "Page.loadEventFired", errorsSeen); err != nil {
		return Shot{}, err
	}
	if err := collectFor(ctx, client, session, options.Settle, errorsSeen); err != nil {
		return Shot{}, err
	}

	var audit struct {
		Result struct {
			Value struct {
				Height   int      `json:"height"`
				Findings []string `json:"findings"`
			} `json:"value"`
		} `json:"result"`
		ExceptionDetails *json.RawMessage `json:"exceptionDetails"`
	}
	expression := auditScript + "(" + strconv.FormatBool(viewport.Mobile) + ", " + strconv.Itoa(viewport.Width) + ")"
	if err := client.call(ctx, session, "Runtime.evaluate", map[string]any{
		"expression": expression, "returnByValue": true, "awaitPromise": true,
	}, &audit); err != nil {
		return Shot{}, err
	}
	height := audit.Result.Value.Height
	if height <= 0 {
		height = viewport.Height
	}
	captured := min(height, options.MaxHeight)
	data, err := screenshot(ctx, client, session, viewport.Width, 0, captured)
	if err != nil {
		return Shot{}, err
	}
	var tiles [][]byte
	spans := tileSpans(captured, viewport.Width)
	if len(spans) == 1 {
		tiles = [][]byte{data}
	} else {
		for _, span := range spans {
			tile, err := screenshot(ctx, client, session, viewport.Width, span[0], span[1])
			if err != nil {
				return Shot{}, err
			}
			tiles = append(tiles, tile)
		}
	}
	// Errors raised while the audit scrolled the page count too.
	_ = collectFor(ctx, client, session, 0, errorsSeen)
	findings := append([]string(nil), audit.Result.Value.Findings...)
	findings = append(findings, errorsSeen.findings()...)
	if height > captured {
		findings = append(findings, fmt.Sprintf("The page is %dpx tall; the screenshot shows the first %dpx.", height, captured))
	}
	return Shot{Page: page.Label, Viewport: viewport, JPEG: data, Height: height, Findings: findings, Tiles: tiles}, nil
}

// screenshot takes a JPEG of the page from top, height pixels tall.
func screenshot(ctx context.Context, client *cdpClient, session string, width, top, height int) ([]byte, error) {
	var picture struct {
		Data string `json:"data"`
	}
	if err := client.call(ctx, session, "Page.captureScreenshot", map[string]any{
		"format": "jpeg", "quality": jpegQuality, "captureBeyondViewport": true, "fromSurface": true,
		"clip": map[string]any{"x": 0, "y": top, "width": width, "height": height, "scale": 1},
	}, &picture); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(picture.Data)
	if err != nil {
		return nil, fmt.Errorf("decode the screenshot: %w", err)
	}
	return data, nil
}

// waitForEvent waits for method on session, noting script errors meanwhile.
func waitForEvent(ctx context.Context, client *cdpClient, session, method string, log *errorLog) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w", method, ctx.Err())
		case <-client.done:
			return errCDPClosed
		case event := <-client.events:
			if event.SessionID != session {
				continue
			}
			if event.Method == method {
				return nil
			}
			log.note(event)
		}
	}
}

// collectFor notes script errors for d, or only those already queued when d
// is zero.
func collectFor(ctx context.Context, client *cdpClient, session string, d time.Duration, log *errorLog) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-client.done:
			return errCDPClosed
		case event := <-client.events:
			if event.SessionID == session {
				log.note(event)
			}
		case <-timer.C:
			if d > 0 {
				return nil
			}
			// d == 0: keep draining what is queued, then stop.
			select {
			case event := <-client.events:
				if event.SessionID == session {
					log.note(event)
				}
				timer.Reset(0)
			default:
				return nil
			}
		}
	}
}

// errorLog keeps the distinct script and loading errors a page raised.
type errorLog struct {
	seen  map[string]struct{}
	order []string
}

func newErrorLog() *errorLog { return &errorLog{seen: map[string]struct{}{}} }

const maxErrorsReported = 5

func (l *errorLog) note(event cdpMessage) {
	var text string
	switch event.Method {
	case "Runtime.exceptionThrown":
		var params struct {
			ExceptionDetails struct {
				Text      string `json:"text"`
				Exception struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		if json.Unmarshal(event.Params, &params) == nil {
			text = params.ExceptionDetails.Exception.Description
			if text == "" {
				text = params.ExceptionDetails.Text
			}
		}
	case "Runtime.consoleAPICalled":
		var params struct {
			Type string `json:"type"`
			Args []struct {
				Value       any    `json:"value"`
				Description string `json:"description"`
			} `json:"args"`
		}
		if json.Unmarshal(event.Params, &params) == nil && params.Type == "error" && len(params.Args) > 0 {
			if params.Args[0].Description != "" {
				text = params.Args[0].Description
			} else {
				text = fmt.Sprint(params.Args[0].Value)
			}
		}
	case "Log.entryAdded":
		var params struct {
			Entry struct {
				Level string `json:"level"`
				Text  string `json:"text"`
				URL   string `json:"url"`
			} `json:"entry"`
		}
		if json.Unmarshal(event.Params, &params) == nil && params.Entry.Level == "error" {
			text = params.Entry.Text
			if params.Entry.URL != "" {
				text += " (" + params.Entry.URL + ")"
			}
		}
	}
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return
	}
	if runes := []rune(text); len(runes) > 200 {
		text = string(runes[:200]) + "…"
	}
	if _, dup := l.seen[text]; dup || len(l.order) >= maxErrorsReported {
		return
	}
	l.seen[text] = struct{}{}
	l.order = append(l.order, text)
}

func (l *errorLog) findings() []string {
	out := make([]string, 0, len(l.order))
	for _, text := range l.order {
		out = append(out, "Browser error: "+text)
	}
	return out
}

// fileURL turns an absolute path into a file:// URL on every platform
// (C:\site\index.html becomes file:///C:/site/index.html).
func fileURL(path string) string {
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}
