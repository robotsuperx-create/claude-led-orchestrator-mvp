package httpd

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// visualServiceFake is a run service that also reports a visual review.
type visualServiceFake struct {
	*claudeOrchestratorAPIFake
	rounds []ports.VisualRound
}

func (f *visualServiceFake) VisualReviewStatus() (bool, string) {
	return false, ports.VisualReasonNoBrowser
}

func (f *visualServiceFake) VisualFor(string) []ports.VisualRound { return f.rounds }

func (f *visualServiceFake) VisualShotFile(_, name string) (string, bool) {
	for _, round := range f.rounds {
		for _, shot := range round.Shots {
			if shot.Name == name {
				return shot.File, true
			}
		}
	}
	return "", false
}

// A person sees the critic's rounds and the pictures it judged; nothing but
// the files the review kept for that run can be fetched.
func TestClaudeOrchestratorAPI_VisualReviewShowsRoundsAndKeptPictures(t *testing.T) {
	dir := t.TempDir()
	picture := filepath.Join(dir, "r1-index-phone.jpg")
	if err := os.WriteFile(picture, []byte{0xFF, 0xD8, 0xFF, 0xD9}, 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("do not serve"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &visualServiceFake{claudeOrchestratorAPIFake: newClaudeOrchestratorAPIFake()}
	fake.result = ports.OrchestrationResult{State: ports.RunStateCompleted, MergeDecision: ports.MergeDecision{Decision: ports.MergeOutcomeMerge}}
	fake.rounds = []ports.VisualRound{
		{Round: 1, Score: 6.5, Scores: map[string]float64{"hierarchy": 7}, Summary: "Phone layout breaks; token sk-ant-abcdefghijklmnopqrstuvwxyz123456 leaked",
			Fixes: []string{"Stack the hero on phones", ""}, Polish: "visual-1", Error: "",
			Shots: []ports.VisualShotRecord{
				{Page: "index.html", Viewport: "phone", Name: "r1-index-phone.jpg", File: picture, Findings: []string{"The page scrolls sideways."}},
				{Page: "notes", Viewport: "desktop", Name: "../secret.txt", File: secret},
			}},
		{Round: 2, Error: "judge the pages: busy"},
	}
	api := &ClaudeOrchestratorAPI{Service: fake, Gate: ports.ClaudeOrchestratorRunPolicy{FeatureEnabled: true}}
	runID := finishedRun(t, api, fake.claudeOrchestratorAPIFake)
	r := newClaudeOrchestratorRouter(api)

	rec := serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/claude-orchestrator/runs/"+runID+"/visual", "", "127.0.0.1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("visual status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, dir) || strings.Contains(body, "sk-ant-") || strings.Contains(body, "busy") || strings.Contains(body, "secret.txt") {
		t.Fatalf("visual review leaked a path, a secret or an error: %s", body)
	}
	var shown ClaudeOrchestratorVisual
	if err := json.Unmarshal(rec.Body.Bytes(), &shown); err != nil {
		t.Fatal(err)
	}
	if len(shown.Rounds) != 2 || shown.Rounds[0].Score != 6.5 || len(shown.Rounds[0].Fixes) != 1 || shown.Rounds[0].Shots[0].Name != "r1-index-phone.jpg" ||
		shown.Rounds[0].Shots[1].Name != "" || !shown.Rounds[1].Failed {
		t.Fatalf("visual = %+v", shown)
	}

	rec = serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/claude-orchestrator/runs/"+runID+"/visual/r1-index-phone.jpg", "", "127.0.0.1", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Body.Len() != 4 {
		t.Fatalf("picture = %d %q, %d bytes", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
	for _, name := range []string{"secret.txt", "r9-index-tablet.jpg", "..%2Fsecret.txt"} {
		rec = serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/claude-orchestrator/runs/"+runID+"/visual/"+name, "", "127.0.0.1", "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", name, rec.Code)
		}
	}
	rec = serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/claude-orchestrator/runs/"+runID+"/visual/r1-index-phone.jpg", "", "127.0.0.1", "https://evil.example")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a browser origin fetched a picture: %d", rec.Code)
	}

	rec = serveClaudeOrchestratorRequest(r, http.MethodGet, "/internal/claude-orchestrator", "", "127.0.0.1", "")
	var info ClaudeOrchestratorInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil || info.VisualReview || info.VisualReason != ports.VisualReasonNoBrowser {
		t.Fatalf("info = %+v, %v", info, err)
	}
}
