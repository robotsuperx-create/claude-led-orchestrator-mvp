// Package x00voice gives X-00 ears and a voice. Claude only reads and writes
// text, so speech goes through engines here: local ones found on this
// computer (Whisper to listen, Piper to speak) and, when the person adds a
// key, a cloud speech service. Each request tries the cloud first and falls
// back to the local engine, so a network problem never leaves him mute.
package x00voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"unicode/utf8"
)

// Limits shared with the app.
const (
	// SampleRate is the only recording rate accepted: 16 kHz mono 16-bit PCM,
	// which both Whisper and the cloud services take as is.
	SampleRate = 16000
	// MaxListenSeconds bounds one recording.
	MaxListenSeconds = 60
	// MaxAudioBytes fits MaxListenSeconds of 16 kHz mono 16-bit PCM plus headers.
	MaxAudioBytes = 44 + SampleRate*2*MaxListenSeconds + 1024
	// MaxSpeakRunes bounds one spoken line.
	MaxSpeakRunes = 3000
	// MaxTranscriptRunes bounds what comes back from listening.
	MaxTranscriptRunes = 4000
)

// Engine kinds reported to the app.
const (
	KindLocal = "local"
	KindCloud = "cloud"
)

var (
	// ErrUnavailable means no engine can do this on this computer yet.
	ErrUnavailable = errors.New("x00voice: no engine available")
	// ErrInvalidAudio means the recording is not 16 kHz mono 16-bit WAV.
	ErrInvalidAudio = errors.New("x00voice: invalid audio")
	// ErrInvalidText means there is nothing (or too much) to say.
	ErrInvalidText = errors.New("x00voice: invalid text")
	// ErrFailed means every available engine failed.
	ErrFailed = errors.New("x00voice: engine failed")
)

// Audio is a spoken line.
type Audio struct {
	Data        []byte
	ContentType string
}

// Ear turns speech into text.
type Ear interface {
	Kind() string
	Available() bool
	Transcribe(ctx context.Context, wav []byte, lang string) (string, error)
}

// Mouth turns text into speech.
type Mouth interface {
	Kind() string
	Available() bool
	Synthesize(ctx context.Context, text, lang string) (Audio, error)
}

// Status says which engine listens and which speaks ("" when none).
type Status struct {
	Listen string `json:"listen,omitempty"`
	Speak  string `json:"speak,omitempty"`
}

// Voice tries its engines in order (cloud first, then local).
type Voice struct {
	ears   []Ear
	mouths []Mouth
}

// New builds a voice from engines in the order they should be tried. Nil
// engines are skipped.
func New(ears []Ear, mouths []Mouth) *Voice {
	voice := &Voice{}
	for _, ear := range ears {
		if ear != nil {
			voice.ears = append(voice.ears, ear)
		}
	}
	for _, mouth := range mouths {
		if mouth != nil {
			voice.mouths = append(voice.mouths, mouth)
		}
	}
	return voice
}

// Status reports the first available engine of each kind. Local engines are
// checked on disk each time, so installing them needs no restart.
func (v *Voice) Status() Status {
	var status Status
	for _, ear := range v.ears {
		if ear.Available() {
			status.Listen = ear.Kind()
			break
		}
	}
	for _, mouth := range v.mouths {
		if mouth.Available() {
			status.Speak = mouth.Kind()
			break
		}
	}
	return status
}

// Listen transcribes a recording in the given language ("ar", "en", or "" to
// let the engine decide).
func (v *Voice) Listen(ctx context.Context, wav []byte, lang string) (string, error) {
	if err := ValidateWAV(wav); err != nil {
		return "", err
	}
	lang = Language(lang)
	tried := false
	for _, ear := range v.ears {
		if !ear.Available() {
			continue
		}
		tried = true
		text, err := ear.Transcribe(ctx, wav, lang)
		if err == nil {
			return clip(strings.TrimSpace(text), MaxTranscriptRunes), nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	if !tried {
		return "", ErrUnavailable
	}
	return "", ErrFailed
}

// Speak voices one line.
func (v *Voice) Speak(ctx context.Context, text, lang string) (Audio, error) {
	text = strings.TrimSpace(text)
	if text == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > MaxSpeakRunes || strings.ContainsRune(text, 0) {
		return Audio{}, ErrInvalidText
	}
	lang = Language(lang)
	if lang == "" {
		lang = DetectLanguage(text)
	}
	tried := false
	for _, mouth := range v.mouths {
		if !mouth.Available() {
			continue
		}
		tried = true
		audio, err := mouth.Synthesize(ctx, text, lang)
		if err == nil && len(audio.Data) > 0 {
			return audio, nil
		}
		if ctx.Err() != nil {
			return Audio{}, ctx.Err()
		}
	}
	if !tried {
		return Audio{}, ErrUnavailable
	}
	return Audio{}, ErrFailed
}

// Language narrows a locale ("ar-MA", "en_US") to the two languages X-00
// speaks well; anything else lets the engine decide.
func Language(locale string) string {
	locale = strings.ToLower(strings.TrimSpace(locale))
	switch {
	case strings.HasPrefix(locale, "ar"):
		return "ar"
	case strings.HasPrefix(locale, "en"):
		return "en"
	default:
		return ""
	}
}

// DetectLanguage says Arabic when the line is written in Arabic script.
func DetectLanguage(text string) string {
	for _, r := range text {
		if (r >= 0x0600 && r <= 0x06FF) || (r >= 0x0750 && r <= 0x077F) || (r >= 0x08A0 && r <= 0x08FF) || (r >= 0xFB50 && r <= 0xFDFF) || (r >= 0xFE70 && r <= 0xFEFF) {
			return "ar"
		}
	}
	return "en"
}

// ValidateWAV accepts only what the app records: a RIFF/WAVE file of 16 kHz
// mono 16-bit PCM, at most MaxListenSeconds long, with some sound in it.
func ValidateWAV(wav []byte) error {
	if len(wav) < 44 || len(wav) > MaxAudioBytes {
		return ErrInvalidAudio
	}
	if !bytes.Equal(wav[0:4], []byte("RIFF")) || !bytes.Equal(wav[8:12], []byte("WAVE")) {
		return ErrInvalidAudio
	}
	var formatOK bool
	var dataBytes int
	for offset := 12; offset+8 <= len(wav); {
		id := string(wav[offset : offset+4])
		size := int(binary.LittleEndian.Uint32(wav[offset+4 : offset+8]))
		body := offset + 8
		if size < 0 || body+size > len(wav) {
			if id == "data" {
				// Some writers leave the data size open; take what is there.
				size = len(wav) - body
			} else {
				return ErrInvalidAudio
			}
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return ErrInvalidAudio
			}
			format := binary.LittleEndian.Uint16(wav[body : body+2])
			channels := binary.LittleEndian.Uint16(wav[body+2 : body+4])
			rate := binary.LittleEndian.Uint32(wav[body+4 : body+8])
			bits := binary.LittleEndian.Uint16(wav[body+14 : body+16])
			formatOK = format == 1 && channels == 1 && rate == SampleRate && bits == 16
		case "data":
			dataBytes = size
		}
		offset = body + size + size%2
	}
	if !formatOK || dataBytes < SampleRate/5*2 || dataBytes > SampleRate*2*MaxListenSeconds {
		return ErrInvalidAudio
	}
	return nil
}

func clip(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit])
}
