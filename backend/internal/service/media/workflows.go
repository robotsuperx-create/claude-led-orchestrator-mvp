package media

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// The built-in capabilities: a picture, a short video, and music or sound.
// A file with the same name in the library's workflows folder replaces one,
// which is how the person points it at the models their server has.
//
//go:embed workflows/*.json
var builtinWorkflows embed.FS

// workflowFile is a capability on disk: a ComfyUI workflow in API format
// ("Export (API)" in ComfyUI) wrapped with what kind of media it makes. Any
// string in the workflow may hold placeholders the gateway fills in:
// {{prompt}}, {{negative}}, {{prefix}} inside text, and {{seed}}, {{width}},
// {{height}}, {{seconds}}, {{frames}}, {{fps}} as whole values (numbers).
// {{image}} names a picture from the library the person picked as input.
type workflowFile struct {
	Version  int              `json:"version"`
	Kind     string           `json:"kind"`
	Title    string           `json:"title,omitempty"`
	Defaults workflowDefaults `json:"defaults"`
	Workflow map[string]any   `json:"workflow"`
}

type workflowDefaults struct {
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	Seconds  int    `json:"seconds,omitempty"`
	Negative string `json:"negative,omitempty"`
}

var capabilityIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

const maxTitleRunes = 80

var errBadWorkflow = errors.New("media: not a workflow the gateway can run")

// parseWorkflow reads and checks one capability file. Numbers stay exactly as
// written (seeds can be larger than a float holds).
func parseWorkflow(data []byte) (workflowFile, error) {
	if len(data) > maxWorkflowBytes {
		return workflowFile{}, errBadWorkflow
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var file workflowFile
	if err := decoder.Decode(&file); err != nil || file.Version != 1 {
		return workflowFile{}, errBadWorkflow
	}
	switch file.Kind {
	case KindImage, KindVideo, KindAudio:
	default:
		return workflowFile{}, errBadWorkflow
	}
	if len(file.Workflow) == 0 || len(file.Workflow) > 500 {
		return workflowFile{}, errBadWorkflow
	}
	// API format is a map of node id to {class_type, inputs}; the UI format
	// (with a "nodes" list) cannot be queued.
	for _, value := range file.Workflow {
		node, ok := value.(map[string]any)
		if !ok {
			return workflowFile{}, errBadWorkflow
		}
		if class, ok := node["class_type"].(string); !ok || class == "" {
			return workflowFile{}, errBadWorkflow
		}
		if _, ok := node["inputs"].(map[string]any); !ok {
			return workflowFile{}, errBadWorkflow
		}
	}
	file.Title = cleanLine(file.Title, maxTitleRunes)
	file.Defaults.Negative = cleanText(file.Defaults.Negative, MaxNegativeRunes)
	return file, nil
}

// workflows reads the built-in capabilities, then the person's own.
func (s *Service) workflows() map[string]workflowFile {
	found := make(map[string]workflowFile)
	builtIns, _ := builtinWorkflows.ReadDir("workflows")
	for _, entry := range builtIns {
		data, err := builtinWorkflows.ReadFile("workflows/" + entry.Name())
		if err != nil {
			continue
		}
		if file, err := parseWorkflow(data); err == nil {
			found[strings.TrimSuffix(entry.Name(), ".json")] = file
		}
	}
	entries, err := os.ReadDir(s.lib.workflowsDir())
	if err != nil {
		return found
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".json")
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") && capabilityIDPattern.MatchString(id) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if len(found) >= MaxCapabilities {
			break
		}
		info, err := os.Lstat(filepath.Join(s.lib.workflowsDir(), name))
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxWorkflowBytes {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.lib.workflowsDir(), name)) //nolint:gosec // a capability file in the library's own folder
		if err != nil {
			continue
		}
		file, err := parseWorkflow(data)
		if err != nil {
			s.log.Warn("media: a workflow in the library could not be used", "file", name)
			continue
		}
		found[strings.TrimSuffix(name, ".json")] = file
	}
	return found
}

// capabilities lists what the gateway can make, built-in ones first.
func (s *Service) capabilities() []Capability {
	builtIns := map[string]bool{}
	entries, _ := builtinWorkflows.ReadDir("workflows")
	for _, entry := range entries {
		builtIns[strings.TrimSuffix(entry.Name(), ".json")] = true
	}
	files := s.workflows()
	list := make([]Capability, 0, len(files))
	for id, file := range files {
		capability := Capability{ID: id, Kind: file.Kind, Title: file.Title, BuiltIn: builtIns[id]}
		if usesPlaceholder(file.Workflow, "{{image}}") {
			capability.Input = KindImage
		}
		list = append(list, capability)
	}
	sort.SliceStable(list, func(a, b int) bool {
		if list[a].BuiltIn != list[b].BuiltIn {
			return list[a].BuiltIn
		}
		return list[a].ID < list[b].ID
	})
	return list
}

// renderValues fill a workflow's placeholders.
type renderValues struct {
	Prompt   string
	Negative string
	Prefix   string
	Image    string
	Seed     int64
	Width    int
	Height   int
	Seconds  int
}

// render returns a fresh copy of the workflow with every placeholder filled.
func render(workflow map[string]any, values renderValues) map[string]any {
	whole := map[string]any{
		"{{seed}}":    values.Seed,
		"{{width}}":   values.Width,
		"{{height}}":  values.Height,
		"{{seconds}}": values.Seconds,
		"{{frames}}":  values.Seconds*videoFPS + 1,
		"{{fps}}":     videoFPS,
	}
	text := strings.NewReplacer("{{prompt}}", values.Prompt, "{{negative}}", values.Negative, "{{prefix}}", values.Prefix, "{{image}}", values.Image)
	var walk func(any) any
	walk = func(value any) any {
		switch typed := value.(type) {
		case map[string]any:
			out := make(map[string]any, len(typed))
			for key, item := range typed {
				out[key] = walk(item)
			}
			return out
		case []any:
			out := make([]any, len(typed))
			for i, item := range typed {
				out[i] = walk(item)
			}
			return out
		case string:
			if number, ok := whole[typed]; ok {
				return number
			}
			return text.Replace(typed)
		default:
			return typed
		}
	}
	rendered, _ := walk(workflow).(map[string]any)
	return rendered
}

// usesPlaceholder is true when any string in the workflow holds the placeholder.
func usesPlaceholder(workflow map[string]any, placeholder string) bool {
	var found bool
	var walk func(any)
	walk = func(value any) {
		if found {
			return
		}
		switch typed := value.(type) {
		case map[string]any:
			for _, item := range typed {
				walk(item)
			}
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case string:
			found = strings.Contains(typed, placeholder)
		}
	}
	walk(workflow)
	return found
}

// cleanText trims and bounds free text, dropping control characters but
// keeping line breaks.
func cleanText(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f || r == utf8.RuneError {
			return -1
		}
		return r
	}, value)
	value = strings.TrimSpace(value)
	if runes := []rune(value); len(runes) > limit {
		value = strings.TrimSpace(string(runes[:limit]))
	}
	return value
}

// cleanLine is cleanText on one line.
func cleanLine(value string, limit int) string {
	return cleanText(strings.ReplaceAll(value, "\n", " "), limit)
}
