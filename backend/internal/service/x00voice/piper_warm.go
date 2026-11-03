package x00voice

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Loading a Piper voice takes one to three seconds on a laptop, longer than
// voicing a sentence. A warm Piper keeps one voice loaded and reads one JSON
// line per utterance ({"text", "output_file"}), printing the file's path when
// it is written. It quits after a quiet spell, and when the daemon goes away
// its standard input closes and it exits on its own.
const warmIdle = 5 * time.Minute

// Session is a running engine fed one line at a time.
type Session interface {
	Send(line string) error
	Lines() <-chan string
	Close()
}

// Starter starts a long-running engine.
type Starter func(command Command) (Session, error)

type processSession struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan string
	once  sync.Once
}

// StartProcess runs an engine as a hidden child process that reads lines.
func StartProcess(command Command) (Session, error) {
	cmd := process.Command(command.Path, command.Args...)
	cmd.Dir = command.Dir
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	session := &processSession{cmd: cmd, stdin: stdin, lines: make(chan string, 8)}
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			session.lines <- strings.TrimSpace(scanner.Text())
		}
		close(session.lines)
		_ = cmd.Wait()
	}()
	return session, nil
}

func (s *processSession) Send(line string) error {
	_, err := io.WriteString(s.stdin, line+"\n")
	return err
}

func (s *processSession) Lines() <-chan string { return s.lines }

func (s *processSession) Close() {
	s.once.Do(func() {
		_ = s.stdin.Close()
		if s.cmd.Process != nil {
			// Give Piper a moment to finish on its own, then make sure.
			go func() {
				time.Sleep(2 * time.Second)
				_ = s.cmd.Process.Kill()
			}()
		}
	})
}

// warmPiper is one loaded voice.
type warmPiper struct {
	mu      sync.Mutex
	session Session
	timer   *time.Timer
	dead    bool
}

type warmPipers struct {
	mu      sync.Mutex
	byVoice map[string]*warmPiper
}

// get returns the warm Piper for a voice, starting it when needed.
func (w *warmPipers) get(start Starter, bin, voice, pace string) (*warmPiper, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.byVoice == nil {
		w.byVoice = make(map[string]*warmPiper)
	}
	key := voice + "\x00" + pace
	if piper := w.byVoice[key]; piper != nil && !piper.isDead() {
		return piper, nil
	}
	session, err := start(Command{
		Path: bin,
		Args: []string{"--model", voice, "--json-input", "--length_scale", pace, "--sentence_silence", "0.25"},
		Dir:  filepath.Dir(bin),
	})
	if err != nil {
		return nil, err
	}
	piper := &warmPiper{session: session}
	piper.timer = time.AfterFunc(warmIdle, piper.retire)
	w.byVoice[key] = piper
	return piper, nil
}

func (p *warmPiper) isDead() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dead
}

func (p *warmPiper) retire() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dead = true
	p.session.Close()
}

// speak voices one line into output and waits for Piper to say it is written.
func (p *warmPiper) speak(ctx context.Context, line, output string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dead {
		return errors.New("piper has stopped")
	}
	p.timer.Reset(warmIdle)
	request, err := json.Marshal(map[string]string{"text": line, "output_file": output})
	if err != nil {
		return err
	}
	if err := p.session.Send(string(request)); err != nil {
		p.dead = true
		p.session.Close()
		return err
	}
	want := filepath.Clean(output)
	for {
		select {
		case got, open := <-p.session.Lines():
			if !open {
				p.dead = true
				return errors.New("piper stopped")
			}
			if filepath.Clean(got) == want {
				return nil
			}
		case <-ctx.Done():
			// A line left half-spoken would answer the next request; start over.
			p.dead = true
			p.session.Close()
			return ctx.Err()
		}
	}
}
