package modelgateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A reply cut off at the output limit is paid for: the edits that were whole
// in it are kept, and the one it stopped in is left out.
func TestTheCompleteEditsOfACutOffReplyAreKept(t *testing.T) {
	cut := `{"summary":"page and styles","more":true,"edits":[` +
		`{"path":"index.html","content":"<main>\n\t<h1>Hi</h1>\n</main>\n"},` +
		`{"path":"css/a.css","replace":[{"old":"a{}","new":"a{color:red}"}]},` +
		`{"path":"js/a.js","content":"\"use strict\";\nfunction half() {`
	kept := completeEdits(cut)
	if kept.Summary != "page and styles" || len(kept.Edits) != 2 {
		t.Fatalf("kept = %+v, want the summary and the two whole edits", kept)
	}
	if kept.Edits[0].Content != "<main>\n\t<h1>Hi</h1>\n</main>\n" || kept.Edits[1].Replace[0].New != "a{color:red}" {
		t.Fatalf("edits = %+v", kept.Edits)
	}
	if empty := completeEdits(`{"summary":"x","edits":[{"path":"a.html","content":"<ma`); len(empty.Edits) != 0 {
		t.Fatalf("an edit cut in the middle was kept: %+v", empty.Edits)
	}
}

func TestProposeEditsContinuesFromACutOffReply(t *testing.T) {
	for name, tc := range map[string]struct {
		content  string
		wantKept int
	}{
		"something complete": {`{"summary":"s","edits":[{"path":"a.css","content":"a{}"},{"path":"b.css","content":"b{`, 1},
		"nothing complete":   {`{"summary":"s","edits":[{"path":"a.css","content":"a{`, 0},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.ReadAll(r.Body)
			encoded, _ := json.Marshal(ChatResponse{Model: "m", Choices: []ChatChoice{{FinishReason: "length", Message: ChatMessage{Role: "assistant", Content: tc.content}}}})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(encoded)
		}))
		proposal, err := claudeAdapterFor(t, server.URL).ProposeEdits(context.Background(), ports.CodeAuthorRequest{Instructions: "x"})
		server.Close()
		if tc.wantKept == 0 {
			if !errors.Is(err, ports.ErrReplyCutOff) {
				t.Fatalf("%s: err = %v, want the cut-off named", name, err)
			}
			continue
		}
		if err != nil || len(proposal.Edits) != tc.wantKept || !proposal.CutOff || !proposal.More {
			t.Fatalf("%s: proposal = %+v, %v; want %d kept edit, marked cut off, more to come", name, proposal, err, tc.wantKept)
		}
	}
}

func TestTheWriterIsGivenTheCrewCharacterAndTheReviewPass(t *testing.T) {
	for _, want := range []string{"X-ROBOT-AI crew", "no placeholders", "never a template's look", "When review is true"} {
		if !strings.Contains(proposeEditsPrompt, want) {
			t.Fatalf("the writer prompt lacks %q", want)
		}
	}
}

func TestTheWriterIsTaughtToWriteLongFilesInParts(t *testing.T) {
	for _, want := range []string{"written in parts", "<!-- continue -->", "/* continue */", "think briefly", "a continue line left in a file"} {
		if !strings.Contains(proposeEditsPrompt, want) {
			t.Fatalf("the writer prompt lacks %q", want)
		}
	}
}
