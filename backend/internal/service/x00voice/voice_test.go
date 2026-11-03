package x00voice

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// testWAV is a 16 kHz mono 16-bit recording of the given length.
func testWAV(t *testing.T, seconds float64, rate uint32, channels uint16) []byte {
	t.Helper()
	samples := int(seconds * float64(rate))
	dataBytes := samples * 2 * int(channels)
	wav := make([]byte, 44+dataBytes)
	copy(wav[0:], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(36+dataBytes))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], channels)
	binary.LittleEndian.PutUint32(wav[24:], rate)
	binary.LittleEndian.PutUint32(wav[28:], rate*uint32(channels)*2)
	binary.LittleEndian.PutUint16(wav[32:], channels*2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], uint32(dataBytes))
	for i := 44; i+1 < len(wav); i += 2 {
		binary.LittleEndian.PutUint16(wav[i:], uint16(i%2000))
	}
	return wav
}

func TestValidateWAVAcceptsOnlyWhatTheAppRecords(t *testing.T) {
	if err := ValidateWAV(testWAV(t, 2, SampleRate, 1)); err != nil {
		t.Fatalf("a 2 s recording was refused: %v", err)
	}
	cases := map[string][]byte{
		"stereo":    testWAV(t, 2, SampleRate, 2),
		"44.1 kHz":  testWAV(t, 2, 44100, 1),
		"too short": testWAV(t, 0.1, SampleRate, 1),
		"too long":  testWAV(t, MaxListenSeconds+1, SampleRate, 1),
		"not a WAV": []byte(strings.Repeat("hello", 40)),
		"empty":     nil,
	}
	for name, wav := range cases {
		if err := ValidateWAV(wav); !errors.Is(err, ErrInvalidAudio) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

type fakeEar struct {
	kind      string
	available bool
	text      string
	err       error
	calls     int
	lang      string
}

func (f *fakeEar) Kind() string    { return f.kind }
func (f *fakeEar) Available() bool { return f.available }
func (f *fakeEar) Transcribe(_ context.Context, _ []byte, lang string) (string, error) {
	f.calls++
	f.lang = lang
	return f.text, f.err
}

type fakeMouth struct {
	kind      string
	available bool
	err       error
	calls     int
	lang      string
}

func (f *fakeMouth) Kind() string    { return f.kind }
func (f *fakeMouth) Available() bool { return f.available }
func (f *fakeMouth) Synthesize(_ context.Context, text, lang string) (Audio, error) {
	f.calls++
	f.lang = lang
	if f.err != nil {
		return Audio{}, f.err
	}
	return Audio{Data: []byte(f.kind + ":" + text), ContentType: "audio/wav"}, nil
}

func TestVoiceFallsBackFromCloudToLocal(t *testing.T) {
	cloud := &fakeEar{kind: KindCloud, available: true, err: errors.New("offline")}
	local := &fakeEar{kind: KindLocal, available: true, text: "  ما حال الأسطول؟  "}
	cloudMouth := &fakeMouth{kind: KindCloud, available: true, err: errors.New("offline")}
	localMouth := &fakeMouth{kind: KindLocal, available: true}
	voice := New([]Ear{cloud, nil, local}, []Mouth{cloudMouth, localMouth})

	if status := voice.Status(); status != (Status{Listen: KindCloud, Speak: KindCloud}) {
		t.Fatalf("status = %+v", status)
	}
	text, err := voice.Listen(context.Background(), testWAV(t, 1, SampleRate, 1), "ar-MA")
	if err != nil || text != "ما حال الأسطول؟" {
		t.Fatalf("Listen = %q, %v", text, err)
	}
	if cloud.calls != 1 || local.calls != 1 || local.lang != "ar" {
		t.Fatalf("cloud calls %d, local calls %d, lang %q", cloud.calls, local.calls, local.lang)
	}
	audio, err := voice.Speak(context.Background(), "مرحباً أيها القائد", "")
	if err != nil || !strings.HasPrefix(string(audio.Data), "local:") || localMouth.lang != "ar" {
		t.Fatalf("Speak = %q, %v (lang %q)", audio.Data, err, localMouth.lang)
	}
}

func TestVoiceSaysWhyItCannot(t *testing.T) {
	none := New([]Ear{&fakeEar{kind: KindLocal}}, []Mouth{&fakeMouth{kind: KindLocal}})
	if status := none.Status(); status != (Status{}) {
		t.Fatalf("status = %+v", status)
	}
	if _, err := none.Listen(context.Background(), testWAV(t, 1, SampleRate, 1), "en"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Listen err = %v", err)
	}
	if _, err := none.Speak(context.Background(), "hello", "en"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Speak err = %v", err)
	}
	broken := New([]Ear{&fakeEar{kind: KindLocal, available: true, err: errors.New("crash")}}, []Mouth{&fakeMouth{kind: KindLocal, available: true, err: errors.New("crash")}})
	if _, err := broken.Listen(context.Background(), testWAV(t, 1, SampleRate, 1), "en"); !errors.Is(err, ErrFailed) {
		t.Fatalf("Listen err = %v", err)
	}
	if _, err := broken.Speak(context.Background(), "hello", "en"); !errors.Is(err, ErrFailed) {
		t.Fatalf("Speak err = %v", err)
	}
	if _, err := broken.Listen(context.Background(), []byte("nope"), "en"); !errors.Is(err, ErrInvalidAudio) {
		t.Fatalf("bad audio err = %v", err)
	}
	for _, text := range []string{"", "   ", strings.Repeat("x", MaxSpeakRunes+1), "a\x00b"} {
		if _, err := broken.Speak(context.Background(), text, "en"); !errors.Is(err, ErrInvalidText) {
			t.Fatalf("Speak(%q) err = %v", text[:min(len(text), 8)], err)
		}
	}
}

func TestLanguageAndDetection(t *testing.T) {
	for locale, want := range map[string]string{"ar": "ar", "ar-MA": "ar", "en_US": "en", "fr": "", "": ""} {
		if got := Language(locale); got != want {
			t.Errorf("Language(%q) = %q", locale, got)
		}
	}
	if DetectLanguage("Run three is held.") != "en" || DetectLanguage("التشغيل الثالث معلّق") != "ar" {
		t.Fatal("DetectLanguage misread a line")
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func argAfter(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

func TestLocalEarRunsWhisperWithTheBestModel(t *testing.T) {
	dir := t.TempDir()
	ear := &LocalEar{Dir: dir, Threads: 2}
	if ear.Available() {
		t.Fatal("available with nothing installed")
	}
	touch(t, filepath.Join(dir, "whisper", executable("whisper-cli")))
	touch(t, filepath.Join(dir, "whisper", "models", "ggml-base-q5_1.bin"))
	touch(t, filepath.Join(dir, "whisper", "models", "ggml-small-q5_1.bin"))
	var seen Command
	ear.Run = func(_ context.Context, command Command) ([]byte, error) {
		seen = command
		input := argAfter(command.Args, "-f")
		if data, err := os.ReadFile(input); err != nil || len(data) < 44 {
			t.Fatalf("whisper input missing: %v", err)
		}
		return nil, os.WriteFile(argAfter(command.Args, "-of")+".txt", []byte(" Start a run\n to add a health check. \n"), 0o600)
	}
	if !ear.Available() {
		t.Fatal("not available once installed")
	}
	text, err := ear.Transcribe(context.Background(), testWAV(t, 1, SampleRate, 1), "en")
	if err != nil || text != "Start a run to add a health check." {
		t.Fatalf("Transcribe = %q, %v", text, err)
	}
	if filepath.Base(argAfter(seen.Args, "-m")) != "ggml-small-q5_1.bin" || argAfter(seen.Args, "-l") != "en" || argAfter(seen.Args, "-t") != "2" || argAfter(seen.Args, "-ac") != "256" {
		t.Fatalf("args = %v", seen.Args)
	}
	if seen.Dir != filepath.Join(dir, "whisper") {
		t.Fatalf("dir = %q", seen.Dir)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "tmp", "*"))
	if len(left) != 0 {
		t.Fatalf("scratch files left behind: %v", left)
	}
	ear.Run = func(_ context.Context, command Command) ([]byte, error) {
		return nil, os.WriteFile(argAfter(command.Args, "-of")+".txt", []byte("[BLANK_AUDIO]\n"), 0o600)
	}
	if text, err := ear.Transcribe(context.Background(), testWAV(t, 1, SampleRate, 1), ""); err != nil || text != "" {
		t.Fatalf("silence = %q, %v", text, err)
	}
}

func TestLocalMouthPicksAVoicePerLanguage(t *testing.T) {
	dir := t.TempDir()
	mouth := &LocalMouth{Dir: dir}
	touch(t, filepath.Join(dir, "piper", executable("piper")))
	if mouth.Available() {
		t.Fatal("available without voices")
	}
	for _, name := range []string{"en_US-ryan-medium.onnx", "en_US-ryan-high.onnx", "ar_JO-kareem-medium.onnx", "ar_JO-orphan-high.onnx"} {
		touch(t, filepath.Join(dir, "piper", "voices", name))
		if name != "ar_JO-orphan-high.onnx" {
			touch(t, filepath.Join(dir, "piper", "voices", name+".json"))
		}
	}
	var seen Command
	mouth.Run = func(_ context.Context, command Command) ([]byte, error) {
		seen = command
		return nil, os.WriteFile(argAfter(command.Args, "--output_file"), testWAV(t, 0.5, 22050, 1), 0o600)
	}
	audio, err := mouth.Synthesize(context.Background(), "مرحباً\nأيها القائد", "ar")
	if err != nil || audio.ContentType != "audio/wav" || len(audio.Data) <= 44 {
		t.Fatalf("Synthesize = %d bytes %q, %v", len(audio.Data), audio.ContentType, err)
	}
	if filepath.Base(argAfter(seen.Args, "--model")) != "ar_JO-kareem-medium.onnx" {
		t.Fatalf("arabic voice = %v", seen.Args)
	}
	if string(seen.Stdin) != "مرحباً أيها القائد\n" {
		t.Fatalf("stdin = %q", seen.Stdin)
	}
	if _, err := mouth.Synthesize(context.Background(), "Hello", "en"); err != nil || filepath.Base(argAfter(seen.Args, "--model")) != "en_US-ryan-medium.onnx" {
		t.Fatalf("english voice = %v, %v", seen.Args, err)
	}
	if _, err := mouth.Synthesize(context.Background(), "Bonjour", "fr"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no french voice: %v", err)
	}
}

func TestCloudEngines(t *testing.T) {
	var transcribeFields map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer voice-key" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/v1/audio/transcriptions":
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				t.Errorf("multipart: %v", err)
			}
			transcribeFields = map[string]string{"model": r.FormValue("model"), "language": r.FormValue("language"), "response_format": r.FormValue("response_format")}
			file, header, err := r.FormFile("file")
			if err != nil || header.Filename != "x00.wav" {
				t.Errorf("file: %v", err)
			} else {
				data, _ := io.ReadAll(file)
				if string(data[:4]) != "RIFF" {
					t.Error("file is not the recording")
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"text": "hello commander"})
		case "/v1/audio/speech":
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["model"] != "gpt-4o-mini-tts" || payload["voice"] != "onyx" || payload["input"] != "Hello" || payload["instructions"] == "" {
				t.Errorf("payload = %v", payload)
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("ID3-mp3"))
		case "/v1/redirect/audio/speech":
			http.Redirect(w, r, "https://example.invalid/steal", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	cfg := CloudConfig{BaseURL: server.URL + "/v1", APIKey: "voice-key", TranscribeModel: "gpt-4o-mini-transcribe", SpeechModel: "gpt-4o-mini-tts", Voice: "onyx"}
	ear := &CloudEar{Config: cfg, Client: server.Client()}
	text, err := ear.Transcribe(context.Background(), testWAV(t, 1, SampleRate, 1), "ar")
	if err != nil || text != "hello commander" {
		t.Fatalf("Transcribe = %q, %v", text, err)
	}
	if transcribeFields["model"] != "gpt-4o-mini-transcribe" || transcribeFields["language"] != "ar" || transcribeFields["response_format"] != "json" {
		t.Fatalf("fields = %v", transcribeFields)
	}
	mouth := &CloudMouth{Config: cfg, Client: server.Client()}
	audio, err := mouth.Synthesize(context.Background(), "Hello", "en")
	if err != nil || string(audio.Data) != "ID3-mp3" || audio.ContentType != "audio/mpeg" {
		t.Fatalf("Synthesize = %q, %v", audio.Data, err)
	}
	wrongKey := &CloudMouth{Config: CloudConfig{BaseURL: cfg.BaseURL, APIKey: "nope", SpeechModel: "tts-1", Voice: "onyx"}, Client: server.Client()}
	if _, err := wrongKey.Synthesize(context.Background(), "Hello", "en"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("a refused key should fail with the status only: %v", err)
	}
	redirected := &CloudMouth{Config: CloudConfig{BaseURL: server.URL + "/v1/redirect", APIKey: "voice-key", SpeechModel: "tts-1", Voice: "onyx"}, Client: NewCloudClient()}
	if _, err := redirected.Synthesize(context.Background(), "Hello", "en"); err == nil {
		t.Fatal("a redirect was followed")
	}
	if (&CloudEar{}).Available() || (&CloudMouth{Config: CloudConfig{APIKey: "k", BaseURL: "u"}}).Available() {
		t.Fatal("a cloud engine without settings claims to be available")
	}
}

func TestAudioContextFitsTheRecording(t *testing.T) {
	for seconds, want := range map[float64]int{1: 256, 5: 384, 9: 576, 25: 1344, 29: 0, 60: 0} {
		if got := audioContext(44 + int(seconds*SampleRate*2)); got != want {
			t.Errorf("audioContext(%vs) = %d, want %d", seconds, got, want)
		}
	}
}

type fakeSession struct {
	mu     sync.Mutex
	lines  chan string
	sent   []string
	closed bool
	stall  bool
	write  func(path string) error
}

func (f *fakeSession) Send(line string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, line)
	var request map[string]string
	if err := json.Unmarshal([]byte(line), &request); err != nil {
		return err
	}
	if f.stall {
		return nil
	}
	if err := f.write(request["output_file"]); err != nil {
		return err
	}
	f.lines <- "some log line"
	f.lines <- request["output_file"]
	return nil
}

func (f *fakeSession) Lines() <-chan string { return f.lines }

func (f *fakeSession) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
}

func TestLocalMouthKeepsAVoiceWarm(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "piper", executable("piper")))
	touch(t, filepath.Join(dir, "piper", "voices", "en_US-ryan-medium.onnx"))
	touch(t, filepath.Join(dir, "piper", "voices", "en_US-ryan-medium.onnx.json"))
	var sessions []*fakeSession
	var started []Command
	mouth := &LocalMouth{Dir: dir, Start: func(command Command) (Session, error) {
		started = append(started, command)
		session := &fakeSession{lines: make(chan string, 8), write: func(path string) error {
			return os.WriteFile(path, testWAV(t, 0.5, 22050, 1), 0o600)
		}}
		sessions = append(sessions, session)
		return session, nil
	}}
	for _, text := range []string{"Welcome, commander.", "The fleet\nis calm."} {
		audio, err := mouth.Synthesize(context.Background(), text, "en")
		if err != nil || len(audio.Data) <= 44 {
			t.Fatalf("Synthesize(%q) = %d bytes, %v", text, len(audio.Data), err)
		}
	}
	if len(started) != 1 || !slices.Contains(started[0].Args, "--json-input") || argAfter(started[0].Args, "--length_scale") != "1.08" {
		t.Fatalf("started = %+v", started)
	}
	var second map[string]string
	_ = json.Unmarshal([]byte(sessions[0].sent[1]), &second)
	if second["text"] != "The fleet is calm." {
		t.Fatalf("second line = %q", second["text"])
	}
	left, _ := filepath.Glob(filepath.Join(dir, "tmp", "*"))
	if len(left) != 0 {
		t.Fatalf("scratch files left behind: %v", left)
	}

	// A stuck Piper is dropped when the request gives up, and the next line starts a fresh one.
	sessions[0].stall = true
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := mouth.Synthesize(ctx, "Are you there?", "en"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled Synthesize err = %v", err)
	}
	if !sessions[0].closed {
		t.Fatal("the stuck Piper was not closed")
	}
	if _, err := mouth.Synthesize(context.Background(), "Back online.", "en"); err != nil || len(started) != 2 {
		t.Fatalf("restart: started %d, err %v", len(started), err)
	}
}
