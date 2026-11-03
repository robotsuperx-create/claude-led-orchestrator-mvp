package x00voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

// CloudConfig is an OpenAI-compatible speech service (the /audio API).
type CloudConfig struct {
	BaseURL         string
	APIKey          string
	TranscribeModel string
	SpeechModel     string
	Voice           string
}

const (
	cloudTimeout        = 60 * time.Second
	maxCloudTranscript  = 256 << 10
	maxCloudSpeechBytes = 16 << 20
	// commanderStyle steers models that take speaking instructions.
	commanderStyle = "You are X-00, the calm commander of a fleet of coding robots. Speak with a deep, steady, warm voice, clearly and a little slowly, like a mission commander. In Arabic, use clear Modern Standard Arabic."
)

// NewCloudClient is an HTTP client for the speech service: bounded, and it
// never follows a redirect with the key attached.
func NewCloudClient() *http.Client {
	return &http.Client{
		Timeout: cloudTimeout,
		// Never follow a redirect with the key attached.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// CloudEar transcribes through /audio/transcriptions.
type CloudEar struct {
	Config CloudConfig
	Client *http.Client
}

// Kind reports that this ear is the cloud service.
func (e *CloudEar) Kind() string { return KindCloud }

// Available reports whether a key, address and model are set.
func (e *CloudEar) Available() bool {
	return e.Config.APIKey != "" && e.Config.BaseURL != "" && e.Config.TranscribeModel != ""
}

// Transcribe sends the recording to the service and returns its text.
func (e *CloudEar) Transcribe(ctx context.Context, wav []byte, lang string) (string, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="x00.wav"`)
	header.Set("Content-Type", "audio/wav")
	part, err := form.CreatePart(header)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(wav); err != nil {
		return "", err
	}
	fields := map[string]string{"model": e.Config.TranscribeModel, "response_format": "json"}
	if lang != "" {
		fields["language"] = lang
	}
	for name, value := range fields {
		if err := form.WriteField(name, value); err != nil {
			return "", err
		}
	}
	if err := form.Close(); err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Config.BaseURL+"/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+e.Config.APIKey)
	data, err := doCloud(e.Client, request, maxCloudTranscript)
	if err != nil {
		return "", err
	}
	var result struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", errors.New("the speech service sent an unreadable transcript")
	}
	return result.Text, nil
}

// CloudMouth speaks through /audio/speech.
type CloudMouth struct {
	Config CloudConfig
	Client *http.Client
}

// Kind reports that this voice is the cloud service.
func (m *CloudMouth) Kind() string { return KindCloud }

// Available reports whether a key, address, model and voice are set.
func (m *CloudMouth) Available() bool {
	return m.Config.APIKey != "" && m.Config.BaseURL != "" && m.Config.SpeechModel != "" && m.Config.Voice != ""
}

// Synthesize asks the service to speak the line and returns MP3 audio.
func (m *CloudMouth) Synthesize(ctx context.Context, text, _ string) (Audio, error) {
	payload := map[string]string{
		"model":           m.Config.SpeechModel,
		"input":           text,
		"voice":           m.Config.Voice,
		"response_format": "mp3",
	}
	if strings.HasPrefix(m.Config.SpeechModel, "gpt-4o") {
		payload["instructions"] = commanderStyle
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Audio{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, m.Config.BaseURL+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		return Audio{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+m.Config.APIKey)
	data, err := doCloud(m.Client, request, maxCloudSpeechBytes)
	if err != nil {
		return Audio{}, err
	}
	return Audio{Data: data, ContentType: "audio/mpeg"}, nil
}

// doCloud sends a request and returns a bounded successful body. Errors never
// carry the service's response, which could echo the key or the audio.
func doCloud(client *http.Client, request *http.Request, limit int64) ([]byte, error) {
	if client == nil {
		client = NewCloudClient()
	}
	response, err := client.Do(request)
	if err != nil {
		if ctxErr := request.Context().Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, errors.New("the speech service could not be reached")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return nil, fmt.Errorf("the speech service answered %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, errors.New("the speech service answer was cut off")
	}
	if int64(len(data)) > limit {
		return nil, errors.New("the speech service answer is too large")
	}
	return data, nil
}
