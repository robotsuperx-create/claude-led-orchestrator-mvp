package scm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestClaudeOrchestratorClientOperationsUseExplicitAPI(t *testing.T) {
	t.Parallel()
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Helper()
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("Authorization = %q, want explicitly supplied bearer credential", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/repos/acme/widget/branches":
			writes.Add(1)
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create branch body: %v", err)
			}
			if body["branch_name"] != "feature/one" || body["base_branch"] != "main" || body["base_sha"] != "base123" {
				t.Errorf("create branch payload = %#v", body)
			}
			_, _ = fmt.Fprint(w, `{"branch_name":"feature/one","commit_sha":"head123"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/repos/acme/widget/pushes":
			writes.Add(1)
			_, _ = fmt.Fprint(w, `{"branch_name":"feature/one","commit_sha":"head123"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/repos/acme/widget/pull-requests":
			writes.Add(1)
			_, _ = fmt.Fprint(w, `{"number":17,"url":"https://scm.invalid/acme/widget/pull/17"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/repos/acme/widget/pull-requests/17/checks":
			if r.URL.Query().Get("head_sha") != "head123" {
				t.Errorf("head_sha query = %q", r.URL.Query().Get("head_sha"))
			}
			_, _ = fmt.Fprint(w, `{"head_sha":"head123","checks":[{"name":"unit","status":"passing","conclusion":"success","url":"https://scm.invalid/check/1","provider_id":"1"}]}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTestSCMClient(t, server.URL+"/api", 2*time.Second, 4096, true)
	repo := ports.SCMRepo{Provider: "example", Host: "scm.invalid", Owner: "acme", Name: "widget", Repo: "acme/widget"}
	approval := func(action ports.ClaudeOrchestratorSCMWriteAction) ports.ClaudeOrchestratorSCMWriteApproval {
		return ports.ClaudeOrchestratorSCMWriteApproval{Action: action, Approved: true}
	}
	ctx := context.Background()

	branch, err := client.CreateBranch(ctx, ports.ClaudeOrchestratorSCMCreateBranchRequest{
		Repository: repo, BranchName: "feature/one", BaseBranch: "main", BaseSHA: "base123",
		Approval: approval(ports.ClaudeOrchestratorSCMCreateBranch),
	})
	if err != nil || branch.BranchName != "feature/one" || branch.CommitSHA != "head123" {
		t.Fatalf("CreateBranch() = %#v, %v", branch, err)
	}
	pushed, err := client.Push(ctx, ports.ClaudeOrchestratorSCMPushRequest{
		Repository: repo, BranchName: "feature/one", CommitSHA: "head123",
		Approval: approval(ports.ClaudeOrchestratorSCMPush),
	})
	if err != nil || pushed.CommitSHA != "head123" {
		t.Fatalf("Push() = %#v, %v", pushed, err)
	}
	pr, err := client.CreatePullRequest(ctx, ports.ClaudeOrchestratorSCMCreatePullRequestRequest{
		Repository: repo, HeadBranch: "feature/one", BaseBranch: "main", Title: "Feature", Body: "Details",
		Approval: approval(ports.ClaudeOrchestratorSCMCreatePR),
	})
	if err != nil || pr.PR.Number != 17 || pr.PR.Repo != repo || pr.PR.URL == "" {
		t.Fatalf("CreatePullRequest() = %#v, %v", pr, err)
	}
	checks, err := client.GetChecks(ctx, ports.ClaudeOrchestratorSCMChecksRequest{PR: pr.PR, HeadSHA: "head123"})
	if err != nil || checks.HeadSHA != "head123" || len(checks.Checks) != 1 || checks.Checks[0].Name != "unit" || checks.Checks[0].Status != "passing" || checks.Checks[0].ProviderID != "1" {
		t.Fatalf("GetChecks() = %#v, %v", checks, err)
	}
	if got := writes.Load(); got != 3 {
		t.Fatalf("external writes = %d, want 3", got)
	}
}

func TestClaudeOrchestratorClientDeniesWritesWithoutMatchingApproval(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	client := newTestSCMClient(t, server.URL, time.Second, 4096, true)
	repo := ports.SCMRepo{Owner: "acme", Name: "repo"}
	requestsToTry := []struct {
		name string
		call func(ports.ClaudeOrchestratorSCMWriteApproval) error
	}{
		{"create branch", func(a ports.ClaudeOrchestratorSCMWriteApproval) error {
			_, err := client.CreateBranch(context.Background(), ports.ClaudeOrchestratorSCMCreateBranchRequest{Repository: repo, BranchName: "b", Approval: a})
			return err
		}},
		{"push", func(a ports.ClaudeOrchestratorSCMWriteApproval) error {
			_, err := client.Push(context.Background(), ports.ClaudeOrchestratorSCMPushRequest{Repository: repo, BranchName: "b", CommitSHA: "sha", Approval: a})
			return err
		}},
		{"create pull request", func(a ports.ClaudeOrchestratorSCMWriteApproval) error {
			_, err := client.CreatePullRequest(context.Background(), ports.ClaudeOrchestratorSCMCreatePullRequestRequest{Repository: repo, HeadBranch: "b", BaseBranch: "main", Title: "title", Approval: a})
			return err
		}},
	}
	for _, test := range requestsToTry {
		t.Run(test.name+"/missing", func(t *testing.T) {
			err := test.call(ports.ClaudeOrchestratorSCMWriteApproval{})
			if !errors.Is(err, ports.ErrClaudeOrchestratorSCMWriteRejected) {
				t.Fatalf("error = %v, want write rejection", err)
			}
		})
	}
	wrongApproval := ports.ClaudeOrchestratorSCMWriteApproval{Action: ports.ClaudeOrchestratorSCMCreateBranch, Approved: true}
	if err := requestsToTry[1].call(wrongApproval); !errors.Is(err, ports.ErrClaudeOrchestratorSCMWriteRejected) {
		t.Fatalf("mismatched approval error = %v, want write rejection", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("unauthorized external requests = %d, want 0", got)
	}
}

func TestClaudeOrchestratorClientWritesDisabledByDefault(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	client := newTestSCMClient(t, server.URL, time.Second, 4096, false)
	_, err := client.Push(context.Background(), ports.ClaudeOrchestratorSCMPushRequest{
		Repository: ports.SCMRepo{Owner: "acme", Name: "repo"}, BranchName: "b", CommitSHA: "sha",
		Approval: ports.ClaudeOrchestratorSCMWriteApproval{Action: ports.ClaudeOrchestratorSCMPush, Approved: true},
	})
	if !errors.Is(err, ports.ErrClaudeOrchestratorSCMWriteRejected) {
		t.Fatalf("error = %v, want write rejection", err)
	}
	if requests.Load() != 0 {
		t.Fatal("writes-disabled client made an HTTP request")
	}
}

func TestClaudeOrchestratorClientHonorsTimeoutAndResponseLimit(t *testing.T) {
	t.Parallel()
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer server.Close()
		client := newTestSCMClient(t, server.URL, 30*time.Millisecond, 4096, false)
		_, err := client.GetChecks(context.Background(), ports.ClaudeOrchestratorSCMChecksRequest{
			PR: ports.SCMPRRef{Repo: ports.SCMRepo{Owner: "acme", Name: "repo"}, Number: 1},
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("GetChecks() error = %v, want deadline exceeded", err)
		}
	})
	t.Run("maximum response bytes", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprint(w, `{"head_sha":"12345678901234567890","checks":[]}`)
		}))
		defer server.Close()
		client := newTestSCMClient(t, server.URL, time.Second, 12, false)
		_, err := client.GetChecks(context.Background(), ports.ClaudeOrchestratorSCMChecksRequest{
			PR: ports.SCMPRRef{Repo: ports.SCMRepo{Owner: "acme", Name: "repo"}, Number: 1},
		})
		if err == nil || !strings.Contains(err.Error(), "exceeds configured maximum") {
			t.Fatalf("GetChecks() error = %v, want bounded response error", err)
		}
	})
}

func TestClaudeOrchestratorClientRedactsCredentialFromErrors(t *testing.T) {
	t.Parallel()
	const secret = "very-secret-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"head_sha":"%s`, secret)
	}))
	defer server.Close()
	client, err := NewClaudeOrchestratorClient(ClaudeOrchestratorClientOptions{
		BaseURL:          server.URL,
		Timeout:          time.Second,
		MaxResponseBytes: 4096,
		Credential:       ports.NewClaudeOrchestratorSCMCredential(secret),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetChecks(context.Background(), ports.ClaudeOrchestratorSCMChecksRequest{
		PR: ports.SCMPRRef{Repo: ports.SCMRepo{Owner: "acme", Name: "repo"}, Number: 1},
	})
	if err == nil {
		t.Fatal("GetChecks() error = nil, want invalid response")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked credential: %v", err)
	}
	if got := fmt.Sprintf("%+v", ports.NewClaudeOrchestratorSCMCredential(secret)); strings.Contains(got, secret) || !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("credential formatting = %q", got)
	}
}

func TestNewClaudeOrchestratorClientRequiresSafeExplicitBaseURL(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "/relative", "ftp://scm.invalid", "http://user:password@scm.invalid", "http://scm.invalid/?token=secret"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := NewClaudeOrchestratorClient(ClaudeOrchestratorClientOptions{BaseURL: raw}); err == nil {
				t.Fatalf("NewClaudeOrchestratorClient(BaseURL=%q) succeeded", raw)
			}
		})
	}
}

func newTestSCMClient(t *testing.T, baseURL string, timeout time.Duration, responseLimit int64, writesEnabled bool) *ClaudeOrchestratorClient {
	t.Helper()
	client, err := NewClaudeOrchestratorClient(ClaudeOrchestratorClientOptions{
		BaseURL:          baseURL,
		Timeout:          timeout,
		MaxResponseBytes: responseLimit,
		Credential:       ports.NewClaudeOrchestratorSCMCredential("test-secret"),
		WritePolicy:      ports.ClaudeOrchestratorSCMWritePolicy{WritesEnabled: writesEnabled},
	})
	if err != nil {
		t.Fatalf("NewClaudeOrchestratorClient(): %v", err)
	}
	return client
}
