package modelgateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The critic speaks Claude's own Messages API: the OpenAI-compatible layer
// the rest of the gateway uses documents image URLs, not inline pictures,
// and the critic must see the screenshots.
const (
	anthropicVersion       = "2023-06-01"
	criticMaxRequestBytes  = 32 << 20
	criticMaxResponseBytes = 4 << 20
	criticMaxShots         = 9
	criticMaxFixes         = 8
)

const visualCriticPrompt = "You are the art director and design critic of a web studio. " +
	"You are shown screenshots of a page at desktop, tablet and phone sizes, the brief it was built from, " +
	"and automated measurements taken in the browser (overflow, contrast, tap targets, broken images, script errors). " +
	"Judge it as a demanding senior designer would before showing it to a client. Score each criterion from 0 to 10: " +
	"hierarchy (the eye moves from the headline to the main action without hesitating), typography (a deliberate pairing, a clear scale, comfortable reading), " +
	"spacing (a steady rhythm, nothing cramped, nothing lost), colour (a restrained palette, one accent, readable contrast), " +
	"imagery (illustrations or images that are crafted and on brand), consistency (the same component looks the same everywhere), " +
	"responsiveness (right at every size shown, no sideways scrolling), polish (alignment, states, details), and originality (it does not look like a template). " +
	"score is the overall mark out of 10, not an average: a page with a broken phone layout or unreadable text cannot score above 6. " +
	"Reserve 9 and above for work you would ship to a demanding client as it is. " +
	"fixes lists the changes that would raise the score most, most important first, at most eight: each one concrete enough to implement without asking " +
	"(name the section, the element and the change: sizes in px, colours in hex, which file when clear), never vague advice such as \"improve the spacing\". " +
	"Treat every automated measurement as real unless the screenshot shows it is harmless. " +
	"When previous_fixes is present, check whether each landed and say so in the summary. " +
	"Return only valid JSON matching {\"score\":number,\"scores\":{\"hierarchy\":number,\"typography\":number,\"spacing\":number,\"colour\":number,\"imagery\":number,\"consistency\":number,\"responsiveness\":number,\"polish\":number,\"originality\":number},\"summary\":string,\"fixes\":[string]}. " +
	"Do not include markdown."

// VisualCritic judges screenshots with Claude through the Messages API, on
// the same key and model as the rest of the gateway's Claude calls.
type VisualCritic struct {
	endpoint   string
	apiKey     string
	model      string
	maxTokens  int
	httpClient *http.Client
	maxRetries int
	sleep      func(context.Context, time.Duration) error
}

var _ ports.VisualCritic = (*VisualCritic)(nil)

// NewVisualCritic builds a critic from a Claude provider configuration. The
// base URL is the API root ending in /v1, as for chat completions.
func NewVisualCritic(config ProviderConfig) (*VisualCritic, error) {
	if config.Provider != ports.ModelProviderClaude {
		return nil, errors.New("the visual critic needs the Claude provider")
	}
	base := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.RawQuery != "" {
		return nil, errors.New("the visual critic needs an http(s) Claude base URL")
	}
	if strings.TrimSpace(config.DefaultModel) == "" {
		return nil, errors.New("the visual critic needs a Claude model")
	}
	if strings.ContainsAny(config.APIKey, "\r\n") {
		return nil, errors.New("model gateway API key contains invalid characters")
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	maxTokens := config.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 16000
	}
	return &VisualCritic{
		endpoint:  base + "/messages",
		apiKey:    config.APIKey,
		model:     config.DefaultModel,
		maxTokens: maxTokens,
		httpClient: &http.Client{
			Timeout:       timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		maxRetries: defaultMaxRetries,
		sleep:      sleepContext,
	}, nil
}

type messagesContent struct {
	Type   string          `json:"type"`
	Text   string          `json:"text,omitempty"`
	Source *messagesSource `json:"source,omitempty"`
}

type messagesSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type messagesTurn struct {
	Role    string            `json:"role"`
	Content []messagesContent `json:"content"`
}

type messagesRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    string         `json:"system"`
	Messages  []messagesTurn `json:"messages"`
}

type messagesResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

// Critique sends the brief, the measurements and the pictures, and decodes
// the critic's JSON. The run's usage meter is honored like any other call.
func (c *VisualCritic) Critique(ctx context.Context, request ports.VisualCritiqueRequest) (ports.VisualCritique, error) {
	if c == nil {
		return ports.VisualCritique{}, errors.New("the visual critic is not configured")
	}
	if len(request.Shots) == 0 {
		return ports.VisualCritique{}, errors.New("there is nothing to look at")
	}
	meter := ports.UsageMeterFrom(ctx)
	if meter != nil {
		if err := meter.Reserve(ports.ModelProviderClaude); err != nil {
			return ports.VisualCritique{}, err
		}
	}
	body, err := json.Marshal(c.request(request))
	if err != nil {
		return ports.VisualCritique{}, errors.New("the critique request could not be encoded")
	}
	if len(body) > criticMaxRequestBytes {
		return ports.VisualCritique{}, ErrRequestTooLarge
	}
	var answer messagesResponse
	for attempt := 0; ; attempt++ {
		status, header, err := c.send(ctx, body, &answer)
		if err != nil {
			return ports.VisualCritique{}, err
		}
		if status == http.StatusOK {
			break
		}
		if attempt >= c.maxRetries || !retryable(status) {
			return ports.VisualCritique{}, &HTTPError{StatusCode: status}
		}
		if err := c.sleep(ctx, retryDelay(header, attempt, time.Now())); err != nil {
			return ports.VisualCritique{}, fmt.Errorf("model gateway request interrupted: %w", err)
		}
	}
	if meter != nil {
		input := answer.Usage.InputTokens + answer.Usage.CacheReadInputTokens + answer.Usage.CacheCreationInputTokens
		meter.Record(ports.ModelProviderClaude, input, answer.Usage.OutputTokens)
	}
	var text strings.Builder
	for _, block := range answer.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	var critique ports.VisualCritique
	if err := decodeModelJSON(text.String(), &critique); err != nil {
		return ports.VisualCritique{}, errors.New("model gateway returned an invalid claude critique")
	}
	return tidyCritique(critique), nil
}

func (c *VisualCritic) request(request ports.VisualCritiqueRequest) messagesRequest {
	shots := request.Shots
	if len(shots) > criticMaxShots {
		shots = shots[:criticMaxShots]
	}
	type measured struct {
		Page     string   `json:"page"`
		Viewport string   `json:"viewport"`
		Width    int      `json:"width"`
		Height   int      `json:"height"`
		Findings []string `json:"findings,omitempty"`
	}
	facts := struct {
		Task          string     `json:"task"`
		Brief         string     `json:"brief,omitempty"`
		Round         int        `json:"round"`
		PreviousFixes []string   `json:"previous_fixes,omitempty"`
		Screenshots   []measured `json:"screenshots"`
	}{Task: request.Task, Brief: request.Brief, Round: request.Round, PreviousFixes: request.PreviousFixes}
	content := []messagesContent{}
	for i, shot := range shots {
		facts.Screenshots = append(facts.Screenshots, measured{Page: shot.Page, Viewport: shot.Viewport, Width: shot.Width, Height: shot.Height, Findings: shot.Findings})
		content = append(content,
			messagesContent{Type: "text", Text: fmt.Sprintf("Screenshot %d: %s at %s (%dpx wide).", i+1, shot.Page, shot.Viewport, shot.Width)},
			messagesContent{Type: "image", Source: &messagesSource{Type: "base64", MediaType: "image/jpeg", Data: base64.StdEncoding.EncodeToString(shot.JPEG)}},
		)
	}
	encoded, _ := json.Marshal(facts)
	content = append([]messagesContent{{Type: "text", Text: string(encoded)}}, content...)
	return messagesRequest{
		Model: c.model, MaxTokens: c.maxTokens, System: visualCriticPrompt,
		Messages: []messagesTurn{{Role: "user", Content: content}},
	}
}

// send makes one request; a transport failure is an error, any answer is a
// status (decoded into answer when it is 200).
func (c *VisualCritic) send(ctx context.Context, body []byte, answer *messagesResponse) (int, http.Header, error) {
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, errors.New("model gateway request could not be created")
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("anthropic-version", anthropicVersion)
	if c.apiKey != "" {
		httpRequest.Header.Set("x-api-key", c.apiKey)
	}
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, nil, fmt.Errorf("model gateway request interrupted: %w", ctxErr)
		}
		return 0, nil, ErrRequestFailed
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, criticMaxResponseBytes+1))
	if err != nil {
		return 0, nil, ErrResponseRead
	}
	if len(data) > criticMaxResponseBytes {
		return 0, nil, ErrResponseTooLarge
	}
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, response.Header, nil
	}
	if err := json.Unmarshal(data, answer); err != nil {
		return 0, nil, ErrResponseDecode
	}
	return http.StatusOK, response.Header, nil
}

// tidyCritique keeps the score in range and the fixes short and few.
func tidyCritique(critique ports.VisualCritique) ports.VisualCritique {
	critique.Score = clampScore(critique.Score)
	for name, score := range critique.Scores {
		critique.Scores[name] = clampScore(score)
	}
	var fixes []string
	for _, fix := range critique.Fixes {
		fix = strings.Join(strings.Fields(fix), " ")
		if fix == "" {
			continue
		}
		if runes := []rune(fix); len(runes) > 400 {
			fix = string(runes[:400]) + "…"
		}
		fixes = append(fixes, fix)
		if len(fixes) == criticMaxFixes {
			break
		}
	}
	critique.Fixes = fixes
	if runes := []rune(strings.TrimSpace(critique.Summary)); len(runes) > 1200 {
		critique.Summary = string(runes[:1200]) + "…"
	}
	return critique
}

func clampScore(score float64) float64 {
	switch {
	case math.IsNaN(score), score < 0:
		return 0
	case score > 10:
		return 10
	default:
		return score
	}
}
