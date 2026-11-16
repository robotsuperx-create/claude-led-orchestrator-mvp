package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Limits on what the GPU server may send back besides the media itself.
const (
	maxServerJSON  = 4 << 20
	maxDeviceRunes = 80
)

// FailUnauthorized means the server refused the token.
const FailUnauthorized = "unauthorized"

// serverError carries one of the Fail codes, never the server's own words.
type serverError struct{ code string }

func (e *serverError) Error() string { return "media: GPU server: " + e.code }

func failure(code string) error { return &serverError{code: code} }

// NewHTTPClient is the client for the GPU server: it never follows a
// redirect, so the token is only ever sent to the address the person set.
// Deadlines come from each request's context.
func NewHTTPClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// server talks to ComfyUI on the person's GPU server.
type server struct {
	base   string
	token  string
	client *http.Client
}

// outputRef names a file ComfyUI saved.
type outputRef struct {
	Filename  string `json:"filename"`
	Subfolder string `json:"subfolder"`
	Type      string `json:"type"`
}

var promptIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// mediaTypes are the only files the library keeps, by extension.
var mediaTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
	".gif":  "image/gif",
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".flac": "audio/flac",
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".opus": "audio/ogg",
	".ogg":  "audio/ogg",
}

func (s *server) request(ctx context.Context, method, route string, body []byte) (*http.Response, error) {
	return s.send(ctx, method, route, body, "application/json")
}

func (s *server) send(ctx context.Context, method, route string, body []byte, contentType string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, s.base+route, reader)
	if err != nil {
		return nil, failure(FailUnreachable)
	}
	if body != nil {
		request.Header.Set("Content-Type", contentType)
	}
	if s.token != "" {
		request.Header.Set("Authorization", "Bearer "+s.token)
	}
	response, err := s.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, failure(FailUnreachable)
	}
	return response, nil
}

// statusFailure turns an unsuccessful answer into a code and drains it.
func statusFailure(response *http.Response) error {
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return failure(FailUnauthorized)
	case response.StatusCode == http.StatusBadRequest:
		// ComfyUI answers 400 when the workflow names a model or node it lacks.
		return failure(FailRejected)
	case response.StatusCode >= 500:
		return failure(FailFailed)
	default:
		return failure(FailUnreachable)
	}
}

func readJSON(response *http.Response, into any) error {
	data, err := io.ReadAll(io.LimitReader(response.Body, maxServerJSON+1))
	if err != nil || len(data) > maxServerJSON {
		return failure(FailFailed)
	}
	if err := json.Unmarshal(data, into); err != nil {
		return failure(FailFailed)
	}
	return nil
}

// submit queues a workflow and returns ComfyUI's prompt id.
func (s *server) submit(ctx context.Context, workflow map[string]any, clientID string) (string, error) {
	body, err := json.Marshal(map[string]any{"prompt": workflow, "client_id": clientID})
	if err != nil {
		return "", failure(FailRejected)
	}
	response, err := s.request(ctx, http.MethodPost, "/prompt", body)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", statusFailure(response)
	}
	var answer struct {
		PromptID string `json:"prompt_id"`
	}
	if err := readJSON(response, &answer); err != nil {
		return "", err
	}
	if !promptIDPattern.MatchString(answer.PromptID) {
		return "", failure(FailFailed)
	}
	return answer.PromptID, nil
}

var uploadNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// upload puts a picture in ComfyUI's input folder and returns the name a
// workflow's LoadImage node refers to it by.
func (s *server) upload(ctx context.Context, name string, data []byte) (string, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("image", name)
	if err != nil {
		return "", failure(FailRejected)
	}
	if _, err := part.Write(data); err != nil {
		return "", failure(FailRejected)
	}
	for field, value := range map[string]string{"type": "input", "overwrite": "true"} {
		if err := form.WriteField(field, value); err != nil {
			return "", failure(FailRejected)
		}
	}
	if err := form.Close(); err != nil {
		return "", failure(FailRejected)
	}
	response, err := s.send(ctx, http.MethodPost, "/upload/image", body.Bytes(), form.FormDataContentType())
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", statusFailure(response)
	}
	var answer struct {
		Name      string `json:"name"`
		Subfolder string `json:"subfolder"`
	}
	if err := readJSON(response, &answer); err != nil {
		return "", err
	}
	if !uploadNamePattern.MatchString(answer.Name) || (answer.Subfolder != "" && !uploadNamePattern.MatchString(answer.Subfolder)) {
		return "", failure(FailFailed)
	}
	if answer.Subfolder != "" {
		return answer.Subfolder + "/" + answer.Name, nil
	}
	return answer.Name, nil
}

// history reports whether the prompt has finished and, if so, the files it
// saved. ComfyUI keeps a prompt in its history only once it has run.
func (s *server) history(ctx context.Context, promptID string) ([]outputRef, bool, error) {
	response, err := s.request(ctx, http.MethodGet, "/history/"+url.PathEscape(promptID), nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, false, statusFailure(response)
	}
	var entries map[string]struct {
		Outputs map[string]map[string]json.RawMessage `json:"outputs"`
		Status  struct {
			StatusStr string `json:"status_str"`
			Completed bool   `json:"completed"`
		} `json:"status"`
	}
	if err := readJSON(response, &entries); err != nil {
		return nil, false, err
	}
	entry, ok := entries[promptID]
	if !ok {
		return nil, false, nil
	}
	if entry.Status.StatusStr == "error" {
		return nil, true, failure(FailFailed)
	}
	nodes := make([]string, 0, len(entry.Outputs))
	for node := range entry.Outputs {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	var refs []outputRef
	for _, node := range nodes {
		keys := make([]string, 0, len(entry.Outputs[node]))
		for key := range entry.Outputs[node] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			var files []outputRef
			// Images, videos and animations all come as lists of saved files;
			// anything else under a node (text, flags) is not media.
			if json.Unmarshal(entry.Outputs[node][key], &files) != nil {
				continue
			}
			for _, file := range files {
				if validOutput(file) {
					refs = append(refs, file)
				}
			}
		}
	}
	return refs, true, nil
}

// validOutput accepts a saved file by name, folder and kind: nothing that
// could climb a path, and only the media the library keeps.
func validOutput(ref outputRef) bool {
	if ref.Type != "output" && ref.Type != "temp" {
		return false
	}
	name := ref.Filename
	if name == "" || len(name) > 255 || strings.ContainsAny(name, "/\\\x00") || name == "." || name == ".." {
		return false
	}
	if _, ok := mediaTypes[strings.ToLower(path.Ext(name))]; !ok {
		return false
	}
	folder := ref.Subfolder
	if len(folder) > 255 || strings.ContainsAny(folder, "\\\x00") || strings.HasPrefix(folder, "/") {
		return false
	}
	for _, part := range strings.Split(folder, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// download copies one saved file, refusing more than limit bytes.
func (s *server) download(ctx context.Context, ref outputRef, into io.Writer, limit int64) (int64, error) {
	query := url.Values{"filename": {ref.Filename}, "subfolder": {ref.Subfolder}, "type": {ref.Type}}
	response, err := s.request(ctx, http.MethodGet, "/view?"+query.Encode(), nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return 0, statusFailure(response)
	}
	if response.ContentLength > limit {
		return 0, failure(FailTooLarge)
	}
	written, err := io.Copy(into, io.LimitReader(response.Body, limit+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return written, ctxErr
		}
		return written, failure(FailUnreachable)
	}
	if written > limit {
		return written, failure(FailTooLarge)
	}
	return written, nil
}

// stats asks the server which card it runs on and how much work it holds.
func (s *server) stats(ctx context.Context) (ServerStatus, error) {
	response, err := s.request(ctx, http.MethodGet, "/system_stats", nil)
	if err != nil {
		return ServerStatus{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return ServerStatus{}, statusFailure(response)
	}
	var stats struct {
		Devices []struct {
			Name      string `json:"name"`
			VRAMTotal int64  `json:"vram_total"`
		} `json:"devices"`
	}
	if err := readJSON(response, &stats); err != nil {
		return ServerStatus{}, err
	}
	status := ServerStatus{Reachable: true}
	if len(stats.Devices) > 0 {
		status.Device = deviceName(stats.Devices[0].Name)
		if stats.Devices[0].VRAMTotal > 0 {
			status.VRAMBytes = stats.Devices[0].VRAMTotal
		}
	}
	if queue, err := s.queueLength(ctx); err == nil {
		status.Queue = queue
	}
	return status, nil
}

func (s *server) queueLength(ctx context.Context) (int, error) {
	response, err := s.request(ctx, http.MethodGet, "/queue", nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return 0, statusFailure(response)
	}
	var queue struct {
		Running []json.RawMessage `json:"queue_running"`
		Pending []json.RawMessage `json:"queue_pending"`
	}
	if err := readJSON(response, &queue); err != nil {
		return 0, err
	}
	return len(queue.Running) + len(queue.Pending), nil
}

// deviceName keeps the card's name readable: "cuda:0 NVIDIA GeForce RTX 4090
// : cudaMallocAsync" becomes "NVIDIA GeForce RTX 4090".
func deviceName(raw string) string {
	name := raw
	if i := strings.Index(name, " "); i > 0 && strings.Contains(name[:i], ":") {
		name = name[i+1:]
	}
	if i := strings.Index(name, " : "); i > 0 {
		name = name[:i]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if runes := []rune(name); len(runes) > maxDeviceRunes {
		name = string(runes[:maxDeviceRunes])
	}
	return name
}

// codeOf reduces any error from a job to a Fail code.
func codeOf(err error) string {
	var failed *serverError
	switch {
	case errors.As(err, &failed):
		return failed.code
	case errors.Is(err, context.DeadlineExceeded):
		return FailTimeout
	default:
		return FailFailed
	}
}
