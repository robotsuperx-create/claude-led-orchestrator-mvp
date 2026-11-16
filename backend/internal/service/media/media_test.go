package media

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

// fakeComfy is a ComfyUI that answers like the real one, behind a token.
type fakeComfy struct {
	t        *testing.T
	mu       sync.Mutex
	token    string
	prompts  []map[string]any
	uploads  []string
	polls    int
	output   outputRef
	body     []byte
	submitAs int
	failRun  bool
}

func (f *fakeComfy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/prompt":
		if f.submitAs != 0 {
			w.WriteHeader(f.submitAs)
			_, _ = io.WriteString(w, `{"error":"model flux1.safetensors not found, token=secret"}`)
			return
		}
		var body struct {
			Prompt   map[string]any `json:"prompt"`
			ClientID string         `json:"client_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("prompt body: %v", err)
		}
		f.prompts = append(f.prompts, body.Prompt)
		_, _ = io.WriteString(w, `{"prompt_id":"p-1","number":1,"node_errors":{}}`)
	case r.Method == http.MethodGet && r.URL.Path == "/history/p-1":
		f.polls++
		if f.polls < 2 {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		status := "success"
		if f.failRun {
			status = "error"
		}
		outputs := map[string]any{"7": map[string]any{"images": []outputRef{f.output, {Filename: "../../etc/passwd.png", Subfolder: "", Type: "output"}}, "text": []string{"hello"}}}
		_ = json.NewEncoder(w).Encode(map[string]any{"p-1": map[string]any{"outputs": outputs, "status": map[string]any{"status_str": status, "completed": true}}})
	case r.Method == http.MethodGet && r.URL.Path == "/view":
		query := r.URL.Query()
		if query.Get("filename") != f.output.Filename || query.Get("subfolder") != f.output.Subfolder || query.Get("type") != "output" {
			f.t.Errorf("view asked for %v", query)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(f.body)
	case r.Method == http.MethodPost && r.URL.Path == "/upload/image":
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			f.t.Errorf("upload: %v", err)
		}
		part, header, err := r.FormFile("image")
		if err != nil {
			f.t.Errorf("upload file: %v", err)
			return
		}
		data, _ := io.ReadAll(part)
		if string(data[:8]) != "\x89PNG\r\n\x1a\n" || r.FormValue("type") != "input" {
			f.t.Errorf("upload = %q type %q", data[:8], r.FormValue("type"))
		}
		f.uploads = append(f.uploads, header.Filename)
		_, _ = io.WriteString(w, `{"name":"`+header.Filename+`","subfolder":"","type":"input"}`)
	case r.URL.Path == "/system_stats":
		_, _ = io.WriteString(w, `{"devices":[{"name":"cuda:0 NVIDIA GeForce RTX 4090 : cudaMallocAsync","type":"cuda","vram_total":25757220864}]}`)
	case r.URL.Path == "/queue":
		_, _ = io.WriteString(w, `{"queue_running":[[1]],"queue_pending":[[2],[3]]}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newGateway(t *testing.T, comfy *fakeComfy) (*Service, string) {
	t.Helper()
	comfy.t = t
	if comfy.token == "" {
		comfy.token = "gpu-token"
	}
	if comfy.output.Filename == "" {
		comfy.output = outputRef{Filename: "xrobot_00001_.png", Subfolder: "xrobot", Type: "output"}
	}
	if comfy.body == nil {
		comfy.body = pngBytes
	}
	httpServer := httptest.NewServer(comfy)
	t.Cleanup(httpServer.Close)
	root := t.TempDir()
	service := New(Config{LibraryDir: root, BaseURL: httpServer.URL, Token: "gpu-token", PollEvery: 5 * time.Millisecond})
	t.Cleanup(service.Close)
	return service, root
}

func waitFor(t *testing.T, service *Service, id string, state string) Item {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		item, err := service.Get(id)
		if err == nil && item.State == state {
			return item
		}
		time.Sleep(5 * time.Millisecond)
	}
	item, _ := service.Get(id)
	t.Fatalf("item %s never reached %s: %+v", id, state, item)
	return Item{}
}

func TestGatewayMakesAPictureOnTheServerAndKeepsIt(t *testing.T) {
	comfy := &fakeComfy{}
	service, root := newGateway(t, comfy)
	item, err := service.Create(Request{Capability: "image", Prompt: "a robot commander on a black bridge", Width: 1000, Source: SourceX00})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item.State != StateQueued || item.Kind != KindImage || item.Width != 992 || item.Height != 1024 || item.Seed <= 0 || item.Source != SourceX00 {
		t.Fatalf("queued item = %+v (sides round down to 16, a seed is chosen)", item)
	}
	done := waitFor(t, service, item.ID, StateDone)
	if len(done.Files) != 1 || done.Files[0].Name != "1.png" || done.Files[0].Type != "image/png" || done.Files[0].Bytes != int64(len(pngBytes)) {
		t.Fatalf("kept files = %+v (the unsafe name is never fetched)", done.Files)
	}
	data, err := os.ReadFile(filepath.Join(root, "items", item.ID, "1.png"))
	if err != nil || string(data) != string(pngBytes) {
		t.Fatalf("library file = %q, %v", data, err)
	}
	comfy.mu.Lock()
	defer comfy.mu.Unlock()
	if len(comfy.prompts) != 1 {
		t.Fatalf("prompts sent = %d", len(comfy.prompts))
	}
	sent, _ := json.Marshal(comfy.prompts[0])
	for _, want := range []string{`"text":"a robot commander on a black bridge"`, `"width":992`, `"filename_prefix":"xrobot/` + item.ID + `"`, `"steps":4`} {
		if !strings.Contains(string(sent), want) {
			t.Errorf("workflow sent lacks %s: %s", want, sent)
		}
	}
	if strings.Contains(string(sent), "{{") {
		t.Errorf("a placeholder reached the server: %s", sent)
	}
}

func TestGatewayReportsTheServerAndItsCapabilities(t *testing.T) {
	service, root := newGateway(t, &fakeComfy{})
	status := service.Status(t.Context())
	if !status.Reachable || status.Device != "NVIDIA GeForce RTX 4090" || status.VRAMBytes != 25757220864 || status.Queue != 3 {
		t.Errorf("status = %+v", status)
	}
	info := service.Info()
	if !info.Enabled || info.Root != root {
		t.Errorf("info = %+v", info)
	}
	kinds := map[string]string{}
	for _, capability := range info.Capabilities {
		kinds[capability.ID] = capability.Kind
	}
	if kinds["image"] != KindImage || kinds["video"] != KindVideo || kinds["music"] != KindAudio || kinds["upscale"] != KindImage || kinds["restyle"] != KindImage || kinds["animate"] != KindVideo {
		t.Errorf("built-in capabilities = %+v", info.Capabilities)
	}
	for _, capability := range info.Capabilities {
		wantsPicture := capability.ID == "upscale" || capability.ID == "restyle" || capability.ID == "animate"
		if capability.BuiltIn != true || (capability.Input == KindImage) != wantsPicture {
			t.Errorf("capability %+v: built in, and takes a picture only for the editing tools", capability)
		}
	}
}

func TestTheServersFailuresBecomeCodesWithoutItsWords(t *testing.T) {
	cases := map[string]struct {
		comfy *fakeComfy
		want  string
	}{
		"missing model":  {&fakeComfy{submitAs: http.StatusBadRequest}, FailRejected},
		"wrong token":    {&fakeComfy{token: "other"}, FailUnauthorized},
		"run failed":     {&fakeComfy{failRun: true}, FailFailed},
		"not a png":      {&fakeComfy{body: []byte("<html>login</html>")}, FailNoOutput},
		"server is down": {&fakeComfy{submitAs: http.StatusBadGateway}, FailFailed},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// The gateway always sends gpu-token; "wrong token" wants another.
			service, _ := newGateway(t, tc.comfy)
			item, err := service.Create(Request{Capability: "image", Prompt: "x"})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			failed := waitFor(t, service, item.ID, StateFailed)
			if failed.Failure != tc.want {
				t.Errorf("failure = %q, want %q", failed.Failure, tc.want)
			}
			data, _ := json.Marshal(failed)
			if strings.Contains(string(data), "secret") || strings.Contains(string(data), "login") {
				t.Errorf("the server's words reached the item: %s", data)
			}
		})
	}
}

func TestAPictureFromTheComputerFeedsACapabilityThatWorksOnOne(t *testing.T) {
	comfy := &fakeComfy{}
	service, root := newGateway(t, comfy)
	workflow := `{"version":1,"kind":"video","title":"Animate a picture","workflow":{
		"1":{"class_type":"LoadImage","inputs":{"image":"{{image}}"}},
		"2":{"class_type":"SaveWEBM","inputs":{"images":["1",0],"filename_prefix":"{{prefix}}","fps":"{{fps}}","seed":"{{seed}}","note":"{{prompt}}"}}}}`
	mustWrite(t, filepath.Join(root, "workflows", "animate.json"), workflow)
	mustWrite(t, filepath.Join(root, "workflows", "ui-export.json"), `{"version":1,"kind":"image","workflow":{"nodes":[1,2]}}`)
	mustWrite(t, filepath.Join(root, "workflows", "Bad Name.json"), workflow)

	var animate *Capability
	for _, capability := range service.Info().Capabilities {
		if capability.ID == "animate" {
			copied := capability
			animate = &copied
		}
		if capability.ID == "ui-export" || capability.ID == "Bad Name" {
			t.Errorf("an unusable workflow became a capability: %+v", capability)
		}
	}
	if animate == nil || animate.Input != KindImage || animate.Title != "Animate a picture" {
		t.Fatalf("animate = %+v", animate)
	}
	if _, err := service.Create(Request{Capability: "animate", Prompt: "slow push in"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("a capability that needs a picture ran without one: %v", err)
	}
	if _, err := service.Upload("shoe.html", []byte("<script>")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("a non-picture was added: %v", err)
	}
	upload, err := service.Upload("shoe.png", pngBytes)
	if err != nil || upload.State != StateDone || upload.Source != SourceUpload || upload.Files[0].Name != "1.png" {
		t.Fatalf("Upload = %+v, %v", upload, err)
	}
	comfy.mu.Lock()
	comfy.output = outputRef{Filename: "xrobot_00001_.webm", Subfolder: "xrobot", Type: "output"}
	comfy.body = append([]byte("\x1a\x45\xdf\xa3"), make([]byte, 32)...)
	comfy.mu.Unlock()
	item, err := service.Create(Request{Capability: "animate", Prompt: "slow push in", Input: upload.ID})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	done := waitFor(t, service, item.ID, StateDone)
	if done.Input != upload.ID || done.Files[0].Type != "video/webm" {
		t.Errorf("animated = %+v", done)
	}
	comfy.mu.Lock()
	defer comfy.mu.Unlock()
	if len(comfy.uploads) != 1 || comfy.uploads[0] != "xrobot-"+item.ID+".png" {
		t.Errorf("uploads = %v", comfy.uploads)
	}
	sent, _ := json.Marshal(comfy.prompts[0])
	if !strings.Contains(string(sent), `"image":"xrobot-`+item.ID+`.png"`) || !strings.Contains(string(sent), `"fps":16`) {
		t.Errorf("workflow sent = %s", sent)
	}
}

func TestRequestsOutOfBoundsAreRefused(t *testing.T) {
	service, _ := newGateway(t, &fakeComfy{})
	for name, request := range map[string]Request{
		"no prompt":       {Capability: "image", Prompt: "  "},
		"prompt too long": {Capability: "image", Prompt: strings.Repeat("a", MaxPromptRunes+1)},
		"too small":       {Capability: "image", Prompt: "x", Width: 64},
		"video too long":  {Capability: "video", Prompt: "x", Seconds: MaxVideoSeconds + 1},
		"music too long":  {Capability: "music", Prompt: "x", Seconds: MaxAudioSeconds + 1},
	} {
		if _, err := service.Create(request); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if _, err := service.Create(Request{Capability: "nope", Prompt: "x"}); !errors.Is(err, ErrUnknownCapability) {
		t.Errorf("unknown capability: %v", err)
	}
}

func TestWithoutAServerTheLibraryStillOpens(t *testing.T) {
	root := t.TempDir()
	service := New(Config{LibraryDir: root, Reason: "no_token"})
	defer service.Close()
	if info := service.Info(); info.Enabled || info.Reason != "no_token" || info.Voice {
		t.Errorf("info = %+v", info)
	}
	if _, err := service.Create(Request{Capability: "image", Prompt: "x"}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("Create = %v", err)
	}
	if status := service.Status(t.Context()); status.Reachable {
		t.Errorf("status = %+v", status)
	}
	items, err := service.List()
	if err != nil || len(items) != 0 {
		t.Errorf("List = %+v, %v", items, err)
	}
}

func TestUnfinishedWorkIsMarkedInterruptedAndDeletingRemovesFiles(t *testing.T) {
	root := t.TempDir()
	lib := &library{root: root}
	if err := lib.ensure(); err != nil {
		t.Fatal(err)
	}
	left := Item{ID: newID(), Kind: KindImage, State: StateRunning, Files: []File{}, CreatedAt: time.Now()}
	if err := lib.save(left); err != nil {
		t.Fatal(err)
	}
	service := New(Config{LibraryDir: root})
	defer service.Close()
	item, err := service.Get(left.ID)
	if err != nil || item.State != StateFailed || item.Failure != FailInterrupted {
		t.Fatalf("left over = %+v, %v", item, err)
	}
	if err := service.Delete(left.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "items", left.ID)); !os.IsNotExist(err) {
		t.Errorf("the item's folder is still there: %v", err)
	}
	if err := service.Delete(left.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Delete = %v", err)
	}
	if err := service.Delete("../../etc"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete of a path = %v", err)
	}
}

func TestOnlySafeOutputsAreFetched(t *testing.T) {
	for _, ref := range []outputRef{
		{Filename: "../x.png", Type: "output"},
		{Filename: "x.png", Subfolder: "../up", Type: "output"},
		{Filename: "x.png", Subfolder: "/abs", Type: "output"},
		{Filename: "x.png", Type: "input"},
		{Filename: "x.exe", Type: "output"},
		{Filename: "x\\y.png", Type: "output"},
	} {
		if validOutput(ref) {
			t.Errorf("accepted %+v", ref)
		}
	}
	if !validOutput(outputRef{Filename: "a_00001_.webm", Subfolder: "xrobot/run", Type: "output"}) {
		t.Error("refused a normal output")
	}
	for _, name := range []string{"1.png", "12.webm", "3.flac"} {
		if !ValidFileName(name) {
			t.Errorf("refused %s", name)
		}
	}
	for _, name := range []string{"item.json", "../1.png", "1.exe", "123.png", ".png"} {
		if ValidFileName(name) {
			t.Errorf("accepted %s", name)
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
