package claudeorchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// PublishRequest names a finished run's branch to share for review. Only a
// person can ask for it; the orchestrator never pushes on its own.
type PublishRequest struct {
	RunID  string
	Branch string
	Title  string
	Body   string
}

// PublishResult says where the branch went and where to review it.
type PublishResult struct {
	Remote string
	Branch string
	Base   string
	// PullRequestURL is set when a pull request was opened.
	PullRequestURL string
	// CompareURL is a link to open the pull request by hand when the GitHub
	// CLI is unavailable or the pull request could not be created.
	CompareURL string
}

// RunPublisher pushes a run branch and opens a pull request for it. It never
// force-pushes, never pushes any other branch, and never merges.
type RunPublisher struct {
	git         WorktreeCommandRunner
	projectRoot string
	remote      string
	prefix      string
}

// Errors returned by RunPublisher.Publish.
var (
	ErrPublishBranchInvalid = errors.New("only a run's own branch can be published")
	ErrPublishNoRemote      = errors.New("the repository has no remote to publish to")
)

var publishRemoteName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// NewRunPublisher publishes branches of projectRoot to remote (default
// "origin").
func NewRunPublisher(git WorktreeCommandRunner, projectRoot, remote string) (*RunPublisher, error) {
	if git == nil {
		return nil, errors.New("run publisher requires a command runner")
	}
	if strings.TrimSpace(projectRoot) == "" {
		return nil, errors.New("run publisher requires the project root")
	}
	remote = strings.TrimSpace(remote)
	if remote == "" {
		remote = "origin"
	}
	if !publishRemoteName.MatchString(remote) {
		return nil, errors.New("run publisher remote name is invalid")
	}
	return &RunPublisher{git: git, projectRoot: projectRoot, remote: remote, prefix: DefaultRunBranchPrefix}, nil
}

// Publish pushes the run's branch (without force) and opens a pull request
// against the remote's default branch with the GitHub CLI when it is
// installed and signed in. Otherwise it returns a compare link.
func (p *RunPublisher) Publish(ctx context.Context, request PublishRequest) (PublishResult, error) {
	if p == nil {
		return PublishResult{}, errors.New("run publisher is not configured")
	}
	if request.Branch != p.prefix+request.RunID || !safeRunID.MatchString(request.RunID) {
		return PublishResult{}, ErrPublishBranchInvalid
	}
	remoteURL, err := p.gitOut(ctx, "remote", "get-url", p.remote)
	if err != nil || strings.TrimSpace(remoteURL) == "" {
		return PublishResult{}, ErrPublishNoRemote
	}
	result := PublishResult{Remote: p.remote, Branch: request.Branch, Base: p.defaultBranch(ctx)}
	// Fail fast instead of waiting on a credential prompt nobody can answer.
	if _, err := p.gitOut(ctx, "-c", "credential.interactive=never", "push", "--no-verify", p.remote, "refs/heads/"+request.Branch+":refs/heads/"+request.Branch); err != nil {
		return result, fmt.Errorf("push %s: %w", request.Branch, err)
	}
	result.CompareURL = githubCompareURL(remoteURL, result.Base, request.Branch)
	title := strings.TrimSpace(request.Title)
	if title == "" {
		title = "X-ROBOT-AI run " + request.RunID
	}
	output, err := p.git.RunInDirectory(ctx, p.projectRoot, []string{
		"gh", "pr", "create",
		"--head", request.Branch,
		"--base", result.Base,
		"--title", title,
		"--body", request.Body,
	})
	if err == nil && output.ExitCode == 0 {
		for _, field := range strings.Fields(output.Stdout) {
			if strings.HasPrefix(field, "https://") {
				result.PullRequestURL = field
			}
		}
	}
	return result, nil
}

// defaultBranch is the remote's default branch, falling back to main.
func (p *RunPublisher) defaultBranch(ctx context.Context) string {
	head, err := p.gitOut(ctx, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+p.remote+"/HEAD")
	if err == nil {
		if branch := strings.TrimPrefix(strings.TrimSpace(head), p.remote+"/"); branch != "" && !strings.ContainsAny(branch, " \t\n") {
			return branch
		}
	}
	return "main"
}

func (p *RunPublisher) gitOut(ctx context.Context, args ...string) (string, error) {
	output, err := p.git.RunInDirectory(ctx, p.projectRoot, append([]string{"git"}, args...))
	if err != nil {
		return "", errors.New(boundedRedacted(err.Error()))
	}
	if output.ExitCode != 0 {
		detail := strings.TrimSpace(output.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(output.Stdout)
		}
		return "", fmt.Errorf("git exited with code %d: %s", output.ExitCode, boundedRedacted(detail))
	}
	return output.Stdout, nil
}

// githubCompareURL turns a GitHub remote into a compare link, or "" for
// other hosts. Credentials in the remote URL are never echoed.
func githubCompareURL(remote, base, branch string) string {
	remote = strings.TrimSpace(remote)
	var path string
	switch {
	case strings.HasPrefix(remote, "git@github.com:"):
		path = strings.TrimPrefix(remote, "git@github.com:")
	default:
		parsed, err := url.Parse(remote)
		if err != nil || !strings.EqualFold(parsed.Hostname(), "github.com") {
			return ""
		}
		path = strings.TrimPrefix(parsed.Path, "/")
	}
	path = strings.TrimSuffix(path, ".git")
	if parts := strings.Split(path, "/"); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return "https://github.com/" + path + "/compare/" + url.PathEscape(base) + "..." + escapeBranch(branch) + "?expand=1"
}

func escapeBranch(branch string) string {
	parts := strings.Split(branch, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
