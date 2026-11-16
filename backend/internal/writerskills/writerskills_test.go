package writerskills

import (
	"strings"
	"testing"
)

func TestLibraryLoadsEverySkill(t *testing.T) {
	summaries := Summaries()
	if len(summaries) < 8 {
		t.Fatalf("library has %d skills, want the full set", len(summaries))
	}
	seen := map[string]bool{}
	for _, summary := range summaries {
		if summary.Name == "" || summary.Description == "" {
			t.Fatalf("skill %+v has no name or description", summary)
		}
		if seen[summary.Name] {
			t.Fatalf("skill %q is listed twice", summary.Name)
		}
		seen[summary.Name] = true
		// The planner reads every description on every plan, so each stays short.
		if len(summary.Description) > 200 {
			t.Errorf("skill %q description is %d bytes; keep it under 200", summary.Name, len(summary.Description))
		}
	}
	for _, name := range []string{"design-taste", "arabic-rtl", "motion", "responsive-accessible", "code-quality"} {
		if !seen[name] {
			t.Errorf("library is missing %q", name)
		}
	}
}

// A writer pays for every word of its skills on every call, so each one is
// focused guidance, not an essay.
func TestEverySkillStaysFocused(t *testing.T) {
	for _, summary := range Summaries() {
		guidance := Guidance([]string{summary.Name})
		if len(guidance) != 1 || guidance[0].Guidance == "" {
			t.Fatalf("Guidance(%q) = %+v", summary.Name, guidance)
		}
		if words := len(strings.Fields(guidance[0].Guidance)); words > 450 {
			t.Errorf("skill %q is %d words; keep it under 450", summary.Name, words)
		}
	}
}

func TestKnownDropsUnknownNamesAndBoundsTheCount(t *testing.T) {
	got := Known([]string{" Design-Taste ", "nope", "motion", "design-taste", "arabic-rtl", "copywriting", "code-quality"})
	want := []string{"design-taste", "motion", "arabic-rtl", "copywriting"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Known = %v, want %v (unknown and repeated names dropped, at most %d)", got, want, MaxPerSubtask)
	}
	if got := Known(nil); len(got) != 0 {
		t.Fatalf("Known(nil) = %v", got)
	}
}

func TestParseRejectsIncompleteSkills(t *testing.T) {
	for name, text := range map[string]string{
		"no front matter": "just text",
		"unterminated":    "---\nname: x\ndescription: y\n",
		"no description":  "---\nname: x\n---\nbody",
		"no guidance":     "---\nname: x\ndescription: y\n---\n   ",
	} {
		if _, err := parse(text); err == nil {
			t.Errorf("parse(%s) accepted an incomplete skill", name)
		}
	}
	parsed, err := parse("---\r\nname: x\r\ndescription: y\r\n---\r\nDo this.\r\n")
	if err != nil || parsed.name != "x" || parsed.description != "y" || parsed.body != "Do this." {
		t.Fatalf("parse with CRLF = %+v, %v", parsed, err)
	}
}
