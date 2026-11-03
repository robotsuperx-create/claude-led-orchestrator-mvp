package x00voice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Local engines live under the speech dir:
//
//	speech/whisper/whisper-cli(.exe)        Whisper (whisper.cpp) and its libraries
//	speech/whisper/models/ggml-*.bin        a Whisper model
//	speech/piper/piper(.exe)                Piper and its espeak-ng-data
//	speech/piper/voices/<lang>_*.onnx       a voice per language, with .onnx.json
//	speech/tmp/                             scratch files for one request
const (
	maxEngineOutput = 32 << 20
	maxTranscript   = 1 << 20
)

// whisperModels is the order models are preferred in: small understands
// Arabic far better than base, and the q5 builds run fastest on a CPU.
var whisperModels = []string{
	"ggml-small-q5_1.bin", "ggml-small-q8_0.bin", "ggml-small.bin",
	"ggml-base-q5_1.bin", "ggml-base-q8_0.bin", "ggml-base.bin",
	"ggml-tiny-q5_1.bin", "ggml-tiny.bin",
}

// Command is one engine invocation.
type Command struct {
	Path  string
	Args  []string
	Dir   string
	Stdin []byte
}

// Runner runs an engine and returns its standard output.
type Runner func(ctx context.Context, command Command) ([]byte, error)

// RunProcess runs an engine as a hidden child process.
func RunProcess(ctx context.Context, command Command) ([]byte, error) {
	cmd := process.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir = command.Dir
	if command.Stdin != nil {
		cmd.Stdin = bytes.NewReader(command.Stdin)
	}
	var stdout limitedBuffer
	stdout.limit = maxEngineOutput
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s failed: %w", filepath.Base(command.Path), err)
	}
	return stdout.Bytes(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := b.limit - b.Len()
	if room > 0 {
		if len(p) > room {
			b.Buffer.Write(p[:room])
		} else {
			b.Buffer.Write(p)
		}
	}
	return len(p), nil
}

func executable(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func scratch(dir, pattern string) (string, error) {
	tmp := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(tmp, pattern)
	if err != nil {
		return "", err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

// LocalEar listens with whisper.cpp.
type LocalEar struct {
	Dir     string
	Run     Runner
	Threads int
}

func (e *LocalEar) Kind() string { return KindLocal }

func (e *LocalEar) paths() (string, string, bool) {
	bin := filepath.Join(e.Dir, "whisper", executable("whisper-cli"))
	if !isFile(bin) {
		return "", "", false
	}
	models := filepath.Join(e.Dir, "whisper", "models")
	for _, name := range whisperModels {
		if path := filepath.Join(models, name); isFile(path) {
			return bin, path, true
		}
	}
	found, _ := filepath.Glob(filepath.Join(models, "ggml-*.bin"))
	sort.Strings(found)
	for _, path := range found {
		if isFile(path) {
			return bin, path, true
		}
	}
	return "", "", false
}

func (e *LocalEar) Available() bool {
	_, _, ok := e.paths()
	return ok
}

func (e *LocalEar) Transcribe(ctx context.Context, wav []byte, lang string) (string, error) {
	bin, model, ok := e.paths()
	if !ok {
		return "", ErrUnavailable
	}
	input, err := scratch(e.Dir, "listen-*.wav")
	if err != nil {
		return "", err
	}
	defer os.Remove(input)
	if err := os.WriteFile(input, wav, 0o600); err != nil {
		return "", err
	}
	base := strings.TrimSuffix(input, ".wav")
	defer os.Remove(base + ".txt")
	if lang == "" {
		lang = "auto"
	}
	threads := e.Threads
	if threads <= 0 {
		threads = max(1, min(8, runtime.NumCPU()))
	}
	args := []string{
		"-m", model, "-f", input, "-l", lang,
		"-t", strconv.Itoa(threads),
		"-nt", "-np", "-otxt", "-of", base,
	}
	if ctx := audioContext(len(wav)); ctx > 0 {
		args = append(args, "-ac", strconv.Itoa(ctx))
	}
	run := e.Run
	if run == nil {
		run = RunProcess
	}
	if _, err := run(ctx, Command{Path: bin, Args: args, Dir: filepath.Dir(bin)}); err != nil {
		return "", err
	}
	data, err := readBounded(base+".txt", maxTranscript)
	if err != nil {
		return "", err
	}
	text := strings.Join(strings.Fields(string(data)), " ")
	if text == "" || isWhisperSilence(text) {
		return "", nil
	}
	return text, nil
}

// audioContext fits Whisper's encoder to the recording. It always encodes a
// 30 s window (1500 frames) unless told otherwise; a short command needs a
// fraction of that, which makes the small model several times faster on a
// laptop CPU. Zero means use the full window.
func audioContext(wavBytes int) int {
	seconds := float64(max(0, wavBytes-44)) / float64(SampleRate*2)
	frames := int((seconds + 1.5) * 50)
	frames = (frames + 63) / 64 * 64
	if frames >= 1500 {
		return 0
	}
	return max(256, frames)
}

// isWhisperSilence spots the captions Whisper invents for silence.
func isWhisperSilence(text string) bool {
	lower := strings.ToLower(strings.Trim(text, " .[]()*"))
	switch lower {
	case "blank_audio", "silence", "music", "no speech", "inaudible":
		return true
	}
	return false
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("engine output is too large")
	}
	return data, nil
}

// LocalMouth speaks with Piper.
type LocalMouth struct {
	Dir string
	Run Runner
}

func (m *LocalMouth) Kind() string { return KindLocal }

func (m *LocalMouth) binary() (string, bool) {
	bin := filepath.Join(m.Dir, "piper", executable("piper"))
	return bin, isFile(bin)
}

// voice picks the best installed voice for a language: high quality first.
func (m *LocalMouth) voice(lang string) (string, bool) {
	found, _ := filepath.Glob(filepath.Join(m.Dir, "piper", "voices", "*.onnx"))
	sort.Strings(found)
	best, bestScore := "", -1
	for _, path := range found {
		if !isFile(path) || !isFile(path+".json") {
			continue
		}
		name := strings.ToLower(filepath.Base(path))
		if !strings.HasPrefix(name, lang+"_") {
			continue
		}
		score := 0
		switch {
		case strings.Contains(name, "-high"):
			score = 3
		case strings.Contains(name, "-medium"):
			score = 2
		case strings.Contains(name, "-low"):
			score = 1
		}
		if score > bestScore {
			best, bestScore = path, score
		}
	}
	return best, best != ""
}

func (m *LocalMouth) Available() bool {
	if _, ok := m.binary(); !ok {
		return false
	}
	_, ar := m.voice("ar")
	_, en := m.voice("en")
	return ar || en
}

func (m *LocalMouth) Synthesize(ctx context.Context, text, lang string) (Audio, error) {
	bin, ok := m.binary()
	if !ok {
		return Audio{}, ErrUnavailable
	}
	if lang == "" {
		lang = DetectLanguage(text)
	}
	voice, ok := m.voice(lang)
	if !ok {
		return Audio{}, ErrUnavailable
	}
	output, err := scratch(m.Dir, "speak-*.wav")
	if err != nil {
		return Audio{}, err
	}
	defer os.Remove(output)
	// Piper reads one utterance per line; one line keeps it one file.
	line := strings.Join(strings.Fields(text), " ")
	// A commander takes his time.
	pace := "1.08"
	if lang == "ar" {
		pace = "1.02"
	}
	args := []string{"--model", voice, "--output_file", output, "--length_scale", pace, "--sentence_silence", "0.25"}
	run := m.Run
	if run == nil {
		run = RunProcess
	}
	if _, err := run(ctx, Command{Path: bin, Args: args, Dir: filepath.Dir(bin), Stdin: []byte(line + "\n")}); err != nil {
		return Audio{}, err
	}
	data, err := readBounded(output, maxEngineOutput)
	if err != nil {
		return Audio{}, err
	}
	if len(data) <= 44 {
		return Audio{}, ErrFailed
	}
	return Audio{Data: data, ContentType: "audio/wav"}, nil
}
