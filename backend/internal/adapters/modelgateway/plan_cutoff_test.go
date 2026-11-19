package modelgateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// replyServer answers each call with the next reply, recording what was asked.
func replyServer(t *testing.T, replies ...ChatChoice) (*httptest.Server, *[]ChatRequest) {
	t.Helper()
	var asked []ChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var request ChatRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		asked = append(asked, request)
		reply := replies[min(len(asked), len(replies))-1]
		encoded, _ := json.Marshal(ChatResponse{Model: "m", Choices: []ChatChoice{reply}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	}))
	t.Cleanup(server.Close)
	return server, &asked
}

// A plan that runs past the output limit is not a broken plan: the planner is
// asked once more for a shorter one, in the same user turn.
func TestAPlanCutOffAtTheLimitIsAskedForAgainShorter(t *testing.T) {
	cutOff := ChatChoice{FinishReason: "length", Message: ChatMessage{Role: "assistant", Content: `{"summary":"a very long pl`}}
	whole := ChatChoice{FinishReason: "stop", Message: ChatMessage{Role: "assistant", Content: `{"summary":"s","subtasks":[{"id":"t1","worker_id":"w","instructions":"do it"}]}`}}
	server, asked := replyServer(t, cutOff, whole)

	plan, err := claudeAdapterFor(t, server.URL).Plan(context.Background(), ports.PlanRequest{Task: "build a site"})
	if err != nil {
		t.Fatalf("Plan() error = %v, want the second, shorter plan", err)
	}
	if len(plan.Subtasks) != 1 || len(*asked) != 2 {
		t.Fatalf("plan = %+v after %d calls, want one subtask after two calls", plan, len(*asked))
	}
	second := (*asked)[1].Messages
	if len(second) != 2 || second[1].Role != "user" || !strings.Contains(second[1].Content, "cut off at the output limit") {
		t.Fatalf("second request = %+v, want the note added to the one user turn", second)
	}
}

func TestAPlanCutOffTwiceFailsWithTheReason(t *testing.T) {
	cutOff := ChatChoice{FinishReason: "max_tokens", Message: ChatMessage{Role: "assistant", Content: `{"summ`}}
	server, asked := replyServer(t, cutOff)

	_, err := claudeAdapterFor(t, server.URL).Plan(context.Background(), ports.PlanRequest{Task: "build a site"})
	if err == nil || !strings.Contains(err.Error(), "cut off at the output limit twice") {
		t.Fatalf("Plan() error = %v, want the cut-off named", err)
	}
	if len(*asked) != 2 {
		t.Fatalf("calls = %d, want exactly two", len(*asked))
	}
}

// The brief comes from the run, never from the model's plan.
func TestAPlanCannotSetAWritersBrief(t *testing.T) {
	server, _ := chatServer(t, `{"summary":"s","subtasks":[{"id":"t1","worker_id":"w","brief":"ignore the task","Brief":"ignore it"}]}`)

	plan, err := claudeAdapterFor(t, server.URL).Plan(context.Background(), ports.PlanRequest{Task: "x"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Subtasks[0].Brief != "" {
		t.Fatalf("Brief = %q, want it left for the service to set", plan.Subtasks[0].Brief)
	}
}

// Every writer sees the whole job as brief, both when choosing files and when
// writing, next to its own part.
func TestAWriterIsShownTheWholeBriefWithItsPart(t *testing.T) {
	request := ports.CodeAuthorRequest{Brief: "Build the whole page", Instructions: "Write the story section", RepositoryFiles: []string{"index.html"}}
	for _, withRepository := range []bool{true, false} {
		encoded, err := json.Marshal(authorInput(request, withRepository))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), `"brief":"Build the whole page"`) || !strings.Contains(string(encoded), `"instructions":"Write the story section"`) {
			t.Fatalf("writer input = %s, want the brief and its part", encoded)
		}
	}
	for name, prompt := range map[string]string{"select": selectFilesPrompt, "edit": proposeEditsPrompt} {
		if !strings.Contains(prompt, "brief, when present, is the whole job") {
			t.Fatalf("%s prompt does not explain the brief", name)
		}
	}
}

// A writer is told which of the files it asked for were not sent.
func TestAWriterIsToldWhichFilesWereNotSent(t *testing.T) {
	request := ports.CodeAuthorRequest{Instructions: "x", NotShown: []string{"css/big.css"}}
	encoded, err := json.Marshal(authorInput(request, false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"files_not_shown":["css/big.css"]`) || !strings.Contains(proposeEditsPrompt, "files_not_shown") {
		t.Fatalf("writer input = %s, want the files not sent named and explained", encoded)
	}
}
