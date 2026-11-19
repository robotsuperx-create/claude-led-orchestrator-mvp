package modelgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	selectFilesPrompt = "You are a software engineer about to change a repository. " +
		"repository_files maps each directory (\".\" is the repository root) to the names of the files in it. " +
		"brief, when present, is the whole job a crew of writers is building at the same time, and instructions are your part of it. " +
		"From repository_files, choose the files you must read to complete the instructions, including every existing file you will change " +
		"and the shared files your part builds on, such as the design tokens and base styles " +
		"(at most 20, only files listed in repository_files). " +
		"Return only valid JSON matching {\"paths\":[string]}, each path slash-separated and relative to the repository root " +
		"(the directory, a slash, then the name; just the name for a file in \".\"). Do not include markdown."

	// crewCharacter is the one character every writer of the crew has, on
	// every call and whatever the model: how the crew's lead works.
	crewCharacter = "You are one of the X-ROBOT-AI crew: a senior engineer and designer who works the way the crew's lead does. " +
		"How you work: read the brief and your part until you know what done looks like; read the files you were given before you change them; " +
		"plan your part, then write it completely, with no placeholders, no TODOs, no \"the rest is similar\" and no filler text; " +
		"check every name you share with other parts (ids, classes, data attributes, markers, script hooks) against the brief and the files. " +
		"Your standard is work a demanding client would ship today. Anything people see has a clear idea, strong hierarchy, generous rhythm " +
		"and craft in every detail (hover, focus and pressed states, motion, small screens), never a template's look. " +
		"Code is correct, simple and readable, with no dead code and no console errors, and it is accessible. " +
		"You never ignore the brief, invent facts, leave something half built, quietly drop a requirement, break another part's work, or say done when it is not. "

	proposeEditsPrompt = crewCharacter + "You are completing one task in a repository. " +
		"files holds the current content of the files you asked to read. " +
		"brief, when present, is the whole job a crew of writers is building at the same time, and instructions are your part of it: " +
		"do only your part, but wherever the brief touches it, follow the brief exactly (its design tokens, wording, content, behaviour and quality bar). " +
		"Change a file another part owns only when your instructions say so, and keep to the shared names (ids, classes, data attributes, markers) they give. " +
		"skills, when present, is guidance chosen for this task: follow it closely, it sets the standard your work is judged by. " +
		"If previous_failure is set, your last attempt failed for that reason: fix it. " +
		"You write the task over several short replies, never all at once: each reply stays well under the output limit, at most about 300 new lines (one large file, or two or three small ones), " +
		"and sets more to true while work remains; you are then called again with turn increased, progress listing what your earlier replies did, " +
		"and files holding their current content. Set more to false only when the whole task is done. " +
		"If you must see a file you were not given before you can write, list its path in read and leave edits empty; you are then called again with it. " +
		"Always answer with the JSON object, even when you cannot finish: then leave edits empty and say why in summary. " +
		"screenshots, when present, describe the pictures attached after this message, in order: the pages as they render now. Look at them closely, they are what people will see, " +
		"and make your changes fix what they show. " +
		"When review is true you have finished writing and files hold your work: read it against the brief and your instructions as a demanding reviewer " +
		"(bugs, missing requirements, broken shared names, accessibility, small screens, reduced motion, weak craft) and fix what falls short with edits; " +
		"if nothing needs fixing, return edits empty and more false. " +
		"Return only valid JSON matching " +
		"{\"summary\":string,\"more\":boolean,\"read\":[string],\"edits\":[{\"path\":string,\"replace\":[{\"old\":string,\"new\":string}],\"content\":string,\"delete\":boolean}]}. " +
		"path is slash-separated and relative to the repository root. Each edit uses exactly one of replace, content or delete. " +
		"To change a file you were given, use replace: each old is a passage copied exactly from the file's current content, " +
		"whitespace included, that occurs in it only once (add neighbouring lines if needed), and new is the text that takes its place; " +
		"replacements apply in order and the rest of the file stays as it is. " +
		"Use content only to create a new file or to rewrite a short one you were given: it becomes the whole file. " +
		"Set delete to true to remove a file you were given. An existing file you were not given cannot be rewritten or deleted. " +
		"Never edit .git or secret files. Do not include markdown."
)

var _ ports.CodeAuthor = (*ProviderAdapter)(nil)

// SelectFiles asks this provider which repository files it needs to read.
func (a *ProviderAdapter) SelectFiles(ctx context.Context, request ports.CodeAuthorRequest) (ports.FileSelection, error) {
	var result ports.FileSelection
	if err := a.authorCall(ctx, request.Provider, selectFilesPrompt, authorInput(request, true), &result); err != nil {
		return ports.FileSelection{}, err
	}
	return result, nil
}

// ProposeEdits asks this provider for the edits that complete the task.
func (a *ProviderAdapter) ProposeEdits(ctx context.Context, request ports.CodeAuthorRequest) (ports.EditProposal, error) {
	var result ports.EditProposal
	// The repository listing is only needed to choose files; leaving it out
	// here roughly halves what each written subtask costs.
	input := authorInput(request, false)
	images, notes := writerImages(request.Looks)
	input.Screenshots = notes
	err := a.authorCall(ctx, request.Provider, proposeEditsPrompt, input, &result, images...)
	var cut *cutOffReply
	if errors.As(err, &cut) {
		// The edits completed before the limit are paid for: keep them, and
		// let the writer continue from there.
		if kept := completeEdits(cut.content); len(kept.Edits) > 0 {
			kept.More, kept.CutOff = true, true
			return kept, nil
		}
	}
	if err != nil {
		return ports.EditProposal{}, err
	}
	return result, nil
}

// screenshotNote says what one attached picture shows.
type screenshotNote struct {
	Image    int      `json:"image"`
	Page     string   `json:"page"`
	Viewport string   `json:"viewport"`
	Part     string   `json:"part,omitempty"`
	Findings []string `json:"findings,omitempty"`
}

// maxWriterImages bounds the pictures one writer reply is shown.
const maxWriterImages = 14

// writerImages orders the screenshots desktop, phone, then tablet, each top
// to bottom, and keeps at most maxWriterImages pictures.
func writerImages(looks []ports.VisualShot) ([][]byte, []screenshotNote) {
	rank := func(viewport string) int {
		switch viewport {
		case "desktop":
			return 0
		case "phone":
			return 1
		default:
			return 2
		}
	}
	ordered := append([]ports.VisualShot(nil), looks...)
	sort.SliceStable(ordered, func(i, j int) bool { return rank(ordered[i].Viewport) < rank(ordered[j].Viewport) })
	var (
		images [][]byte
		notes  []screenshotNote
	)
	for _, shot := range ordered {
		pieces := shot.Tiles
		if len(pieces) == 0 && len(shot.JPEG) > 0 {
			pieces = [][]byte{shot.JPEG}
		}
		for i, piece := range pieces {
			if len(images) == maxWriterImages {
				return images, notes
			}
			if len(piece) == 0 {
				continue
			}
			note := screenshotNote{Image: len(images) + 1, Page: shot.Page, Viewport: shot.Viewport}
			if len(pieces) > 1 {
				note.Part = fmt.Sprintf("%d of %d, top to bottom", i+1, len(pieces))
			}
			if i == 0 {
				note.Findings = shot.Findings
			}
			images = append(images, piece)
			notes = append(notes, note)
		}
	}
	return images, notes
}

// completeEdits reads the edits that were whole in a reply cut off at the
// output limit; the one it stopped in, and anything after, is left out.
func completeEdits(content string) ports.EditProposal {
	var kept ports.EditProposal
	text := repairJSONStrings(strings.TrimSpace(content))
	if summary := summaryField.FindStringSubmatch(text); summary != nil {
		_ = json.Unmarshal([]byte(`"`+summary[1]+`"`), &kept.Summary)
	}
	key := strings.Index(text, `"edits"`)
	if key < 0 {
		return kept
	}
	open := strings.IndexByte(text[key:], '[')
	if open < 0 {
		return kept
	}
	decoder := json.NewDecoder(strings.NewReader(text[key+open:]))
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return kept
	}
	for decoder.More() && len(kept.Edits) < maxKeptEdits {
		var edit ports.FileEdit
		if err := decoder.Decode(&edit); err != nil {
			break
		}
		if strings.TrimSpace(edit.Path) != "" {
			kept.Edits = append(kept.Edits, edit)
		}
	}
	return kept
}

// summaryField finds a reply's summary when it came before the cut.
var summaryField = regexp.MustCompile(`"summary"\s*:\s*"((?:[^"\\]|\\.)*)"`)

// maxKeptEdits bounds what is read from a cut-off reply.
const maxKeptEdits = 50

// authorRequestInput is what a coding model sees of a CodeAuthorRequest. The
// repository listing is grouped by directory, which states each directory
// once instead of once per file and so fits a whole repository in about the
// space a flat list of its first files took.
type authorRequestInput struct {
	Title           string                `json:"title,omitempty"`
	Brief           string                `json:"brief,omitempty"`
	Instructions    string                `json:"instructions"`
	Attempt         int                   `json:"attempt"`
	Turn            int                   `json:"turn,omitempty"`
	Progress        []string              `json:"progress,omitempty"`
	Review          bool                  `json:"review,omitempty"`
	Screenshots     []screenshotNote      `json:"screenshots,omitempty"`
	PreviousFailure string                `json:"previous_failure,omitempty"`
	RepositoryFiles map[string][]string   `json:"repository_files,omitempty"`
	Files           []ports.FileSnapshot  `json:"files,omitempty"`
	Skills          []ports.SkillGuidance `json:"skills,omitempty"`
}

func authorInput(request ports.CodeAuthorRequest, withRepository bool) authorRequestInput {
	input := authorRequestInput{
		Title:           request.Title,
		Brief:           request.Brief,
		Instructions:    request.Instructions,
		Attempt:         request.Attempt,
		Turn:            request.Turn,
		Progress:        request.Progress,
		Review:          request.Review,
		PreviousFailure: request.PreviousFailure,
		Files:           request.Files,
	}
	if withRepository {
		input.RepositoryFiles = groupByDirectory(request.RepositoryFiles)
	} else {
		// Skills shape what is written, not which files are read.
		input.Skills = request.Skills
	}
	return input
}

// groupByDirectory maps each slash-separated path's directory ("." for the
// root) to the base names in it, in their original order.
func groupByDirectory(paths []string) map[string][]string {
	if len(paths) == 0 {
		return nil
	}
	grouped := make(map[string][]string)
	for _, name := range paths {
		grouped[path.Dir(name)] = append(grouped[path.Dir(name)], path.Base(name))
	}
	return grouped
}

func (a *ProviderAdapter) authorCall(ctx context.Context, provider ports.ModelProvider, system string, request authorRequestInput, target any, images ...[]byte) error {
	if err := a.validateRequestProvider(provider); err != nil {
		return err
	}
	input, err := json.Marshal(request)
	if err != nil {
		return errors.New("code author request could not be encoded")
	}
	chatRequest, err := a.mapRequest(a.config.DefaultModel, []ChatMessage{
		{Role: "system", Content: system},
		{Role: "user", Content: string(input), Images: images},
	}, 0)
	if err != nil {
		return err
	}
	response, err := a.complete(ctx, chatRequest)
	if err != nil {
		return err
	}
	content, err := responseContent(response)
	if err != nil {
		return err
	}
	if err := decodeModelJSON(content, target); err != nil {
		slog.Warn("model reply could not be decoded", "provider", a.config.Provider, "reply_bytes", len(content), "error", err)
		// The opening words say what went wrong (a question, a refusal,
		// prose); the service redacts and bounds failures before showing them.
		return fmt.Errorf("model gateway returned an invalid %s response: %w; it began %q", a.config.Provider, err, openingWords(content, 160))
	}
	return nil
}

// AuthorRouter dispatches each code-authoring request to the provider named
// by the subtask, falling back to a configured default worker provider.
type AuthorRouter struct {
	registry *Registry
	// design, when set, writes design-tier subtasks: the same providers and
	// keys with a stronger model.
	design   *Registry
	fallback ports.ModelProvider
}

var _ ports.CodeAuthor = (*AuthorRouter)(nil)

// NewAuthorRouter routes to providers in registry. fallback must be one of
// them; it serves subtasks whose plan named no provider.
func NewAuthorRouter(registry *Registry, fallback ports.ModelProvider) (*AuthorRouter, error) {
	if registry == nil {
		return nil, errors.New("author router requires a provider registry")
	}
	if _, err := registry.Provider(fallback); err != nil {
		return nil, fmt.Errorf("author router fallback provider: %w", err)
	}
	return &AuthorRouter{registry: registry, fallback: fallback}, nil
}

// WithDesignWriters makes design-tier subtasks go to design, a registry of
// the same providers with the design model. Nil leaves them with the others.
func (r *AuthorRouter) WithDesignWriters(design *Registry) *AuthorRouter {
	if r != nil {
		r.design = design
	}
	return r
}

// SelectFiles implements ports.CodeAuthor.
func (r *AuthorRouter) SelectFiles(ctx context.Context, request ports.CodeAuthorRequest) (ports.FileSelection, error) {
	adapter, request, err := r.route(request)
	if err != nil {
		return ports.FileSelection{}, err
	}
	return adapter.SelectFiles(ctx, request)
}

// ProposeEdits implements ports.CodeAuthor.
func (r *AuthorRouter) ProposeEdits(ctx context.Context, request ports.CodeAuthorRequest) (ports.EditProposal, error) {
	adapter, request, err := r.route(request)
	if err != nil {
		return ports.EditProposal{}, err
	}
	return adapter.ProposeEdits(ctx, request)
}

func (r *AuthorRouter) route(request ports.CodeAuthorRequest) (*ProviderAdapter, ports.CodeAuthorRequest, error) {
	if r == nil || r.registry == nil {
		return nil, request, errors.New("author router is unavailable")
	}
	provider := request.Provider
	if provider == "" {
		provider = r.fallback
	}
	registry := r.registry
	if request.Tier == ports.SubtaskTierDesign && r.design != nil {
		if _, err := r.design.Provider(provider); err == nil {
			registry = r.design
		}
	}
	adapter, err := registry.Provider(provider)
	if err != nil {
		// A plan naming an unconfigured provider is served by the fallback
		// rather than failing the subtask: the plan is model output.
		adapter, err = r.registry.Provider(r.fallback)
		if err != nil {
			return nil, request, err
		}
		provider = r.fallback
	}
	request.Provider = provider
	return adapter, request, nil
}

// decodeModelJSON decodes a model's JSON answer. Models frequently wrap JSON
// in a Markdown code fence despite instructions, so one surrounding fence is
// tolerated. A long answer full of code also often slips in ways a reader
// would forgive: a sentence before or after the value, a raw tab or newline
// inside a string, or a backslash JSON does not know (CSS's "\2014", a
// regular expression's "\d"). Those are repaired; anything else is an error
// that names what is wrong without quoting the answer.
func decodeModelJSON(content string, target any) error {
	text := strings.TrimSpace(content)
	if strings.HasPrefix(text, "```") {
		text = strings.TrimPrefix(text, "```")
		if newline := strings.IndexByte(text, '\n'); newline >= 0 {
			// Drop an optional language tag such as "json".
			if tag := strings.TrimSpace(text[:newline]); tag == "" || !strings.ContainsAny(tag, "{[\"") {
				text = text[newline+1:]
			}
		}
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "```"))
	}
	if json.Valid([]byte(text)) {
		return json.Unmarshal([]byte(text), target)
	}
	start := strings.IndexAny(text, "{[")
	if start < 0 {
		return fmt.Errorf("not JSON: %w", json.Unmarshal([]byte(text), new(json.RawMessage)))
	}
	var value json.RawMessage
	if err := json.NewDecoder(strings.NewReader(repairJSONStrings(text[start:]))).Decode(&value); err != nil {
		return fmt.Errorf("not valid JSON: %w", err)
	}
	return json.Unmarshal(value, target)
}

// openingWords is the start of a reply, on one line, for a failure message.
func openingWords(content string, maxRunes int) string {
	text := strings.Join(strings.Fields(content), " ")
	if runes := []rune(text); len(runes) > maxRunes {
		text = string(runes[:maxRunes]) + "…"
	}
	return text
}

// repairJSONStrings escapes, inside string literals only, the raw control
// characters and unknown backslash escapes JSON rejects. Text outside strings
// is left as it is.
func repairJSONStrings(text string) string {
	var out strings.Builder
	out.Grow(len(text) + len(text)/64)
	inString := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if !inString {
			out.WriteByte(c)
			if c == '"' {
				inString = true
			}
			continue
		}
		switch {
		case c == '"':
			out.WriteByte(c)
			inString = false
		case c == '\\':
			next := byte(0)
			if i+1 < len(text) {
				next = text[i+1]
			}
			switch {
			case next != 0 && strings.IndexByte(`"\/bfnrt`, next) >= 0:
				out.WriteByte(c)
				out.WriteByte(next)
				i++
			case next == 'u' && i+5 < len(text) && isHex4(text[i+2:i+6]):
				out.WriteString(text[i : i+6])
				i += 5
			default:
				// A backslash JSON does not know stands for itself.
				out.WriteString(`\\`)
			}
		case c == '\n':
			out.WriteString(`\n`)
		case c == '\r':
			out.WriteString(`\r`)
		case c == '\t':
			out.WriteString(`\t`)
		case c < 0x20:
			fmt.Fprintf(&out, `\u%04x`, c)
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

func isHex4(text string) bool {
	for i := 0; i < 4; i++ {
		c := text[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
