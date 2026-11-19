package modelgateway

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A long answer full of code slips in ways a reader forgives; the writer's
// work is not thrown away for them.
func TestAModelAnswerWithForgivableSlipsIsRepaired(t *testing.T) {
	cases := map[string]string{
		"raw tab and newline in a string": "{\"summary\":\"s\",\"edits\":[{\"path\":\"css/a.css\",\"content\":\"a {\n\tcolor: red;\n}\"}]}",
		"CSS escape":                      `{"summary":"s","edits":[{"path":"css/a.css","content":"q::before { content: \"\2014\"; }"}]}`,
		"regular expression escape":       `{"summary":"s","edits":[{"path":"js/a.js","content":"const re = /^\d+\.\s*$/;"}]}`,
		"a sentence around the value":     "Here are the edits:\n{\"summary\":\"s\",\"edits\":[{\"path\":\"a.txt\",\"content\":\"hi\"}]}\nLet me know if you need more.",
	}
	want := map[string]string{
		"raw tab and newline in a string": "a {\n\tcolor: red;\n}",
		"CSS escape":                      `q::before { content: "\2014"; }`,
		"regular expression escape":       `const re = /^\d+\.\s*$/;`,
		"a sentence around the value":     "hi",
	}
	for name, content := range cases {
		var proposal ports.EditProposal
		if err := decodeModelJSON(content, &proposal); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(proposal.Edits) != 1 || proposal.Edits[0].Content != want[name] {
			t.Fatalf("%s: edits = %+v, want content %q", name, proposal.Edits, want[name])
		}
	}
}

func TestValidEscapesSurviveTheRepairUnchanged(t *testing.T) {
	text := `{"a":"quote \" slash \\ solidus \/ tab \t unicode é €","b":"\uZZZZ"}`
	var value map[string]string
	if err := decodeModelJSON(text, &value); err != nil {
		t.Fatal(err)
	}
	if value["a"] != "quote \" slash \\ solidus / tab \t unicode é €" || value["b"] != `\uZZZZ` {
		t.Fatalf("value = %q", value)
	}
}

// What cannot be repaired is named without quoting the answer back.
func TestAnAnswerThatIsNotJSONSaysWhatIsWrong(t *testing.T) {
	var proposal ports.EditProposal
	err := decodeModelJSON("I could not complete this task with secret-token-123.", &proposal)
	if err == nil || !strings.Contains(err.Error(), "not JSON") || strings.Contains(err.Error(), "secret-token-123") {
		t.Fatalf("err = %v, want a short reason without the answer", err)
	}
	err = decodeModelJSON(`{"summary":"half`, &proposal)
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("err = %v, want the JSON named invalid", err)
	}
}
