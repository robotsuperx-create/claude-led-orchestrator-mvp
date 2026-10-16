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

// chatServer returns an OpenAI-compatible endpoint whose assistant message is
// content, exactly as a real provider returns model text. It records the last
// request body so tests can assert what was sent to the provider.
func chatServer(t *testing.T, content string) (*httptest.Server, *string) {
	t.Helper()
	var lastBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		lastBody = string(body)
		encoded, err := json.Marshal(content)
		if err != nil {
			t.Errorf("encode content: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":`+string(encoded)+`},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)
	return server, &lastBody
}

func claudeAdapterFor(t *testing.T, baseURL string) *ProviderAdapter {
	t.Helper()
	return newTestAdapter(t, ProviderConfig{Provider: ports.ModelProviderClaude, BaseURL: baseURL, DefaultModel: "m"})
}

// The system prompt asks the model for snake_case keys such as "worker_id".
// Decoding that text must populate every typed field; previously WorkerID
// stayed empty and every real plan was rejected as having no worker.
func TestPlanDecodesModelJSONMatchingThePromptSchema(t *testing.T) {
	server, _ := chatServer(t, `{"summary":"Add tests","subtasks":[`+
		`{"id":"t1","title":"Write tests","instructions":"cover the parser","worker_id":"deepseek-worker","provider":"deepseek"},`+
		`{"id":"t2","title":"Fix bug","instructions":"fix it","worker_id":"claude-worker"}]}`)

	plan, err := claudeAdapterFor(t, server.URL).Plan(context.Background(), ports.PlanRequest{Task: "add tests"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Summary != "Add tests" || len(plan.Subtasks) != 2 {
		t.Fatalf("plan = %+v, want summary and two subtasks", plan)
	}
	first := plan.Subtasks[0]
	if first.ID != "t1" || first.Title != "Write tests" || first.Instructions != "cover the parser" ||
		first.WorkerID != "deepseek-worker" || first.Provider != ports.ModelProviderDeepSeek {
		t.Fatalf("first subtask = %+v, want every prompt field decoded", first)
	}
	if plan.Subtasks[1].WorkerID != "claude-worker" {
		t.Fatalf("second subtask worker = %q, want claude-worker", plan.Subtasks[1].WorkerID)
	}
}

// Metadata carries server-trusted values (the worktree path). A model must not
// be able to set it, even if it adds the key to its JSON.
func TestPlanIgnoresModelSuppliedMetadata(t *testing.T) {
	server, _ := chatServer(t, `{"summary":"s","subtasks":[{"id":"t1","worker_id":"w",`+
		`"metadata":{"worktree_path":"/home/user/.ssh"},"Metadata":{"worktree_path":"/etc"}}]}`)

	plan, err := claudeAdapterFor(t, server.URL).Plan(context.Background(), ports.PlanRequest{Task: "x"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Subtasks[0].Metadata != nil {
		t.Fatalf("Metadata = %v, want nil: model output must not set trusted metadata", plan.Subtasks[0].Metadata)
	}
}

func TestReviewDecodesModelJSONMatchingThePromptSchema(t *testing.T) {
	server, _ := chatServer(t, `{"decision":"request_changes","summary":"tests fail","issues":["fix parser"]}`)

	decision, err := claudeAdapterFor(t, server.URL).Review(context.Background(), ports.ReviewRequest{Task: "x"})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if decision.Decision != ports.ReviewOutcomeRequestChanges || decision.Summary != "tests fail" ||
		len(decision.Issues) != 1 || decision.Issues[0] != "fix parser" {
		t.Fatalf("decision = %+v, want all prompt fields decoded", decision)
	}
}

// The review request is serialized to the provider. It must use the same
// snake_case vocabulary as the plan prompt and must never leak the local
// worktree path held in trusted metadata.
func TestReviewRequestUsesWireNamesAndOmitsTrustedMetadata(t *testing.T) {
	server, lastBody := chatServer(t, `{"decision":"approve","summary":"ok","issues":[]}`)
	const localPath = "/Users/someone/private/worktree"
	task := ports.PlannedSubtask{ID: "t1", WorkerID: "w", Metadata: map[string]string{ports.SubtaskMetadataKeyWorktreePath: localPath}}

	_, err := claudeAdapterFor(t, server.URL).Review(context.Background(), ports.ReviewRequest{
		Task: "x",
		Plan: ports.ExecutionPlan{Summary: "s", Subtasks: []ports.PlannedSubtask{task}},
		Results: []ports.CollectedTaskResult{{
			Task:           task,
			FinalExecution: ports.WorkerExecution{Status: ports.WorkerOutcomeCompleted},
		}},
		Validation: ports.ValidationReport{Passed: true},
	})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if strings.Contains(*lastBody, localPath) || strings.Contains(*lastBody, "worktree_path") {
		t.Fatalf("provider request leaked trusted metadata: %s", *lastBody)
	}
	for _, key := range []string{`\"worker_id\":\"w\"`, `\"final_execution\"`, `\"passed\":true`} {
		if !strings.Contains(*lastBody, key) {
			t.Fatalf("provider request missing %s: %s", key, *lastBody)
		}
	}
}
