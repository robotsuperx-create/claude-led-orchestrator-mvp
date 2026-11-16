// Package writerskills is mission control's library of skills: short, focused
// guidance (design taste, Arabic typography, motion, copywriting, ...) that
// the planner assigns to each subtask and that the writer then receives with
// that subtask only. Each skill is a Markdown file with a name and a one-line
// description in its front matter, the same shape as an Agent Skill's
// SKILL.md, so the library can grow without code changes.
//
// The planner sees every skill's name and description; a writer sees the full
// text of the few skills its subtask names. That keeps each call small.
package writerskills

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// MaxPerSubtask bounds how many skills one subtask carries, which bounds
// what each writing call costs.
const MaxPerSubtask = 4

//go:embed library/*.md
var library embed.FS

var (
	loadOnce sync.Once
	loaded   []skill
	loadErr  error
)

type skill struct {
	name        string
	description string
	body        string
}

func load() ([]skill, error) {
	loadOnce.Do(func() {
		entries, err := fs.ReadDir(library, "library")
		if err != nil {
			loadErr = err
			return
		}
		for _, entry := range entries {
			if entry.IsDir() || path.Ext(entry.Name()) != ".md" {
				continue
			}
			data, err := library.ReadFile("library/" + entry.Name())
			if err != nil {
				loadErr = err
				return
			}
			parsed, err := parse(string(data))
			if err != nil {
				loadErr = fmt.Errorf("skill %s: %w", entry.Name(), err)
				return
			}
			loaded = append(loaded, parsed)
		}
		sort.Slice(loaded, func(i, j int) bool { return loaded[i].name < loaded[j].name })
	})
	return loaded, loadErr
}

// parse reads a skill file: front matter with name and description between
// two "---" lines, then the guidance.
func parse(text string) (skill, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		return skill{}, fmt.Errorf("missing front matter")
	}
	header, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return skill{}, fmt.Errorf("unterminated front matter")
	}
	var parsed skill
	for _, line := range strings.Split(header, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "name":
			parsed.name = strings.TrimSpace(value)
		case "description":
			parsed.description = strings.TrimSpace(value)
		}
	}
	parsed.body = strings.TrimSpace(body)
	if parsed.name == "" || parsed.description == "" || parsed.body == "" {
		return skill{}, fmt.Errorf("a skill needs a name, a description and guidance")
	}
	return parsed, nil
}

// Summaries lists every skill's name and description, for the planner.
func Summaries() []ports.SkillSummary {
	skills, err := load()
	if err != nil {
		return nil
	}
	out := make([]ports.SkillSummary, 0, len(skills))
	for _, s := range skills {
		out = append(out, ports.SkillSummary{Name: s.name, Description: s.description})
	}
	return out
}

// Known keeps the names that are in the library, in order, without
// duplicates, and at most MaxPerSubtask of them. A plan naming a skill that
// does not exist is not an error: the name is simply dropped.
func Known(names []string) []string {
	skills, err := load()
	if err != nil {
		return nil
	}
	exists := make(map[string]struct{}, len(skills))
	for _, s := range skills {
		exists[s.name] = struct{}{}
	}
	var out []string
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if _, ok := exists[name]; !ok {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
		if len(out) == MaxPerSubtask {
			break
		}
	}
	return out
}

// Guidance returns the full text of the named skills, for a writer.
func Guidance(names []string) []ports.SkillGuidance {
	skills, err := load()
	if err != nil {
		return nil
	}
	byName := make(map[string]skill, len(skills))
	for _, s := range skills {
		byName[s.name] = s
	}
	var out []ports.SkillGuidance
	for _, name := range Known(names) {
		s := byName[name]
		out = append(out, ports.SkillGuidance{Name: s.name, Guidance: s.body})
	}
	return out
}
