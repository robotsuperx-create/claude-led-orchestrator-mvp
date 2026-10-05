package ports_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.ClaudeOrchestratorSCM = (*fakeClaudeOrchestratorSCM)(nil)

type fakeClaudeOrchestratorSCM struct {
	policy         ports.ClaudeOrchestratorSCMWritePolicy
	branchRequests []ports.ClaudeOrchestratorSCMCreateBranchRequest
	pushRequests   []ports.ClaudeOrchestratorSCMPushRequest
	pullRequests   []ports.ClaudeOrchestratorSCMCreatePullRequestRequest
	checksRequests []ports.ClaudeOrchestratorSCMChecksRequest
}

func (f *fakeClaudeOrchestratorSCM) CreateBranch(_ context.Context, request ports.ClaudeOrchestratorSCMCreateBranchRequest) (ports.ClaudeOrchestratorSCMBranchResult, error) {
	if err := f.policy.Authorize(request.Approval, ports.ClaudeOrchestratorSCMCreateBranch); err != nil {
		return ports.ClaudeOrchestratorSCMBranchResult{}, err
	}
	f.branchRequests = append(f.branchRequests, request)
	return ports.ClaudeOrchestratorSCMBranchResult{BranchName: request.BranchName, CommitSHA: request.BaseSHA}, nil
}

func (f *fakeClaudeOrchestratorSCM) Push(_ context.Context, request ports.ClaudeOrchestratorSCMPushRequest) (ports.ClaudeOrchestratorSCMPushResult, error) {
	if err := f.policy.Authorize(request.Approval, ports.ClaudeOrchestratorSCMPush); err != nil {
		return ports.ClaudeOrchestratorSCMPushResult{}, err
	}
	f.pushRequests = append(f.pushRequests, request)
	return ports.ClaudeOrchestratorSCMPushResult{BranchName: request.BranchName, CommitSHA: request.CommitSHA}, nil
}

func (f *fakeClaudeOrchestratorSCM) CreatePullRequest(_ context.Context, request ports.ClaudeOrchestratorSCMCreatePullRequestRequest) (ports.ClaudeOrchestratorSCMCreatePullRequestResult, error) {
	if err := f.policy.Authorize(request.Approval, ports.ClaudeOrchestratorSCMCreatePR); err != nil {
		return ports.ClaudeOrchestratorSCMCreatePullRequestResult{}, err
	}
	f.pullRequests = append(f.pullRequests, request)
	return ports.ClaudeOrchestratorSCMCreatePullRequestResult{
		PR: ports.SCMPRRef{Repo: request.Repository, Number: 17, URL: "https://scm.example/acme/widget/pull/17"},
	}, nil
}

func (f *fakeClaudeOrchestratorSCM) GetChecks(_ context.Context, request ports.ClaudeOrchestratorSCMChecksRequest) (ports.ClaudeOrchestratorSCMChecksResult, error) {
	f.checksRequests = append(f.checksRequests, request)
	return ports.ClaudeOrchestratorSCMChecksResult{
		HeadSHA: request.HeadSHA,
		Checks:  []ports.SCMCheckObservation{{Name: "unit", Status: "passing", URL: "https://scm.example/checks/unit"}},
	}, nil
}

func TestClaudeOrchestratorSCMWritePolicyDefaultsToDeny(t *testing.T) {
	var fake fakeClaudeOrchestratorSCM
	repo := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "acme", Name: "widget", Repo: "acme/widget"}

	_, err := fake.CreateBranch(context.Background(), ports.ClaudeOrchestratorSCMCreateBranchRequest{
		Repository: repo, BranchName: "agent/change", BaseBranch: "main", BaseSHA: "base",
		Approval: ports.ClaudeOrchestratorSCMWriteApproval{Action: ports.ClaudeOrchestratorSCMCreateBranch, Approved: true},
	})
	assertSCMWriteRejected(t, err, ports.ClaudeOrchestratorSCMCreateBranch, ports.ClaudeOrchestratorSCMWritesDisabled)

	_, err = fake.Push(context.Background(), ports.ClaudeOrchestratorSCMPushRequest{
		Repository: repo, BranchName: "agent/change", CommitSHA: "commit",
		Approval: ports.ClaudeOrchestratorSCMWriteApproval{Action: ports.ClaudeOrchestratorSCMPush, Approved: true},
	})
	assertSCMWriteRejected(t, err, ports.ClaudeOrchestratorSCMPush, ports.ClaudeOrchestratorSCMWritesDisabled)

	_, err = fake.CreatePullRequest(context.Background(), ports.ClaudeOrchestratorSCMCreatePullRequestRequest{
		Repository: repo, HeadBranch: "agent/change", BaseBranch: "main", Title: "Change",
		Approval: ports.ClaudeOrchestratorSCMWriteApproval{Action: ports.ClaudeOrchestratorSCMCreatePR, Approved: true},
	})
	assertSCMWriteRejected(t, err, ports.ClaudeOrchestratorSCMCreatePR, ports.ClaudeOrchestratorSCMWritesDisabled)

	if len(fake.branchRequests)+len(fake.pushRequests)+len(fake.pullRequests) != 0 {
		t.Fatal("fake recorded a write that the default policy should deny")
	}
}

func TestClaudeOrchestratorSCMRequiresMatchingApprovalForEveryWrite(t *testing.T) {
	fake := fakeClaudeOrchestratorSCM{policy: ports.ClaudeOrchestratorSCMWritePolicy{WritesEnabled: true}}
	repo := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "acme", Name: "widget", Repo: "acme/widget"}

	_, err := fake.CreateBranch(context.Background(), ports.ClaudeOrchestratorSCMCreateBranchRequest{Repository: repo, BranchName: "agent/change"})
	assertSCMWriteRejected(t, err, ports.ClaudeOrchestratorSCMCreateBranch, ports.ClaudeOrchestratorSCMApprovalRequired)

	_, err = fake.Push(context.Background(), ports.ClaudeOrchestratorSCMPushRequest{Repository: repo, BranchName: "agent/change", CommitSHA: "commit"})
	assertSCMWriteRejected(t, err, ports.ClaudeOrchestratorSCMPush, ports.ClaudeOrchestratorSCMApprovalRequired)

	_, err = fake.CreatePullRequest(context.Background(), ports.ClaudeOrchestratorSCMCreatePullRequestRequest{Repository: repo, HeadBranch: "agent/change", BaseBranch: "main", Title: "Change"})
	assertSCMWriteRejected(t, err, ports.ClaudeOrchestratorSCMCreatePR, ports.ClaudeOrchestratorSCMApprovalRequired)

	_, err = fake.Push(context.Background(), ports.ClaudeOrchestratorSCMPushRequest{
		Repository: repo, BranchName: "agent/change", CommitSHA: "commit",
		Approval: ports.ClaudeOrchestratorSCMWriteApproval{Action: ports.ClaudeOrchestratorSCMCreateBranch, Approved: true},
	})
	assertSCMWriteRejected(t, err, ports.ClaudeOrchestratorSCMPush, ports.ClaudeOrchestratorSCMApprovalActionMismatch)

	if len(fake.branchRequests)+len(fake.pushRequests)+len(fake.pullRequests) != 0 {
		t.Fatal("fake recorded a write without matching per-operation approval")
	}
}

func TestClaudeOrchestratorSCMFakeTypedContracts(t *testing.T) {
	ctx := context.Background()
	repo := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "acme", Name: "widget", Repo: "acme/widget"}
	fake := fakeClaudeOrchestratorSCM{policy: ports.ClaudeOrchestratorSCMWritePolicy{WritesEnabled: true}}

	branch, err := fake.CreateBranch(ctx, ports.ClaudeOrchestratorSCMCreateBranchRequest{
		Repository: repo, BranchName: "agent/change", BaseBranch: "main", BaseSHA: "base-sha",
		Approval: ports.ClaudeOrchestratorSCMWriteApproval{Action: ports.ClaudeOrchestratorSCMCreateBranch, Approved: true},
	})
	if err != nil || branch.BranchName != "agent/change" || branch.CommitSHA != "base-sha" {
		t.Fatalf("CreateBranch() = %+v, %v; want typed branch result", branch, err)
	}

	pushed, err := fake.Push(ctx, ports.ClaudeOrchestratorSCMPushRequest{
		Repository: repo, BranchName: branch.BranchName, CommitSHA: "commit-sha",
		Approval: ports.ClaudeOrchestratorSCMWriteApproval{Action: ports.ClaudeOrchestratorSCMPush, Approved: true},
	})
	if err != nil || pushed.BranchName != "agent/change" || pushed.CommitSHA != "commit-sha" {
		t.Fatalf("Push() = %+v, %v; want typed push result", pushed, err)
	}

	created, err := fake.CreatePullRequest(ctx, ports.ClaudeOrchestratorSCMCreatePullRequestRequest{
		Repository: repo, HeadBranch: "agent/change", BaseBranch: "main", Title: "Add change", Body: "Details", Draft: true,
		Approval: ports.ClaudeOrchestratorSCMWriteApproval{Action: ports.ClaudeOrchestratorSCMCreatePR, Approved: true},
	})
	if err != nil || created.PR.Number != 17 || created.PR.Repo.Repo != "acme/widget" {
		t.Fatalf("CreatePullRequest() = %+v, %v; want typed PR ref", created, err)
	}

	checks, err := fake.GetChecks(ctx, ports.ClaudeOrchestratorSCMChecksRequest{PR: created.PR, HeadSHA: "commit-sha"})
	if err != nil || checks.HeadSHA != "commit-sha" || len(checks.Checks) != 1 || checks.Checks[0].Name != "unit" {
		t.Fatalf("GetChecks() = %+v, %v; want normalized typed checks", checks, err)
	}
	if len(fake.branchRequests) != 1 || len(fake.pushRequests) != 1 || len(fake.pullRequests) != 1 || len(fake.checksRequests) != 1 {
		t.Fatalf("fake call counts = branch %d, push %d, PR %d, checks %d; want one each", len(fake.branchRequests), len(fake.pushRequests), len(fake.pullRequests), len(fake.checksRequests))
	}
}

func TestClaudeOrchestratorSCMCredentialRedaction(t *testing.T) {
	secret := "ghp-secret-test-value"
	credential := ports.NewClaudeOrchestratorSCMCredential(secret)

	for _, format := range []string{"%s", "%v", "%+v", "%#v", "%q"} {
		formatted := fmt.Sprintf(format, credential)
		if strings.Contains(formatted, secret) || formatted != "[REDACTED]" {
			t.Errorf("fmt.Sprintf(%q, credential) = %q; want redacted output", format, formatted)
		}
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		t.Fatalf("json.Marshal(credential): %v", err)
	}
	if strings.Contains(string(encoded), secret) || string(encoded) != `"[REDACTED]"` {
		t.Fatalf("json.Marshal(credential) = %s; want a redacted JSON string", encoded)
	}
	if credential.Value() != secret {
		t.Fatal("explicit adapter Value() did not return the injected credential")
	}
}

func assertSCMWriteRejected(t *testing.T, err error, action ports.ClaudeOrchestratorSCMWriteAction, reason ports.ClaudeOrchestratorSCMWriteRejectionReason) {
	t.Helper()
	if err == nil {
		t.Fatal("SCM write error = nil; want typed rejection")
	}
	var rejection *ports.ClaudeOrchestratorSCMWriteRejectedError
	if !errors.As(err, &rejection) {
		t.Fatalf("SCM write error type = %T; want *ClaudeOrchestratorSCMWriteRejectedError", err)
	}
	if !errors.Is(err, ports.ErrClaudeOrchestratorSCMWriteRejected) {
		t.Fatalf("SCM write error %v does not wrap rejection sentinel", err)
	}
	if rejection.Action != action || rejection.Reason != reason {
		t.Fatalf("rejection = {Action:%q Reason:%q}; want {%q %q}", rejection.Action, rejection.Reason, action, reason)
	}
}
