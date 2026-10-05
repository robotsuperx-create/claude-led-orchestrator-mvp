// Package scm contains provider-neutral SCM adapters.
package scm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	defaultClaudeOrchestratorSCMTimeout          = 15 * time.Second
	defaultClaudeOrchestratorSCMMaxResponseBytes = 1 << 20
)

// ClaudeOrchestratorClientOptions configures the generic JSON HTTP API used by
// ClaudeOrchestratorClient. BaseURL and credentials are injected explicitly;
// this adapter never reads environment variables or discovers credentials.
type ClaudeOrchestratorClientOptions struct {
	BaseURL          string
	HTTPClient       *http.Client
	Timeout          time.Duration
	MaxResponseBytes int64
	Credential       ports.ClaudeOrchestratorSCMCredential
	WritePolicy      ports.ClaudeOrchestratorSCMWritePolicy
}

// ClaudeOrchestratorClient implements the orchestrator's typed SCM boundary
// using a small provider-neutral REST API rooted at BaseURL:
//
//	POST /repos/{owner}/{name}/branches
//	POST /repos/{owner}/{name}/pushes
//	POST /repos/{owner}/{name}/pull-requests
//	GET  /repos/{owner}/{name}/pull-requests/{number}/checks?head_sha=...
//
// JSON field names correspond to the typed port fields. Each write is denied
// unless the configured policy is enabled and that request carries matching,
// affirmative action-scoped approval.
type ClaudeOrchestratorClient struct {
	baseURL          *url.URL
	httpClient       *http.Client
	timeout          time.Duration
	maxResponseBytes int64
	credential       ports.ClaudeOrchestratorSCMCredential
	writePolicy      ports.ClaudeOrchestratorSCMWritePolicy
}

var _ ports.ClaudeOrchestratorSCM = (*ClaudeOrchestratorClient)(nil)

// NewClaudeOrchestratorClient validates explicit configuration and returns a
// client. Zero timeout and response limit select conservative bounded defaults.
// The default transport does not consult proxy environment variables.
func NewClaudeOrchestratorClient(opts ClaudeOrchestratorClientOptions) (*ClaudeOrchestratorClient, error) {
	base, err := url.Parse(strings.TrimSpace(opts.BaseURL))
	if err != nil || base == nil || !base.IsAbs() || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, errors.New("claude orchestrator SCM: BaseURL must be an absolute http(s) URL")
	}
	if base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("claude orchestrator SCM: BaseURL must not contain credentials, query, or fragment")
	}
	if opts.Timeout < 0 {
		return nil, errors.New("claude orchestrator SCM: timeout must not be negative")
	}
	if opts.MaxResponseBytes < 0 {
		return nil, errors.New("claude orchestrator SCM: maximum response size must not be negative")
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultClaudeOrchestratorSCMTimeout
	}
	maxResponseBytes := opts.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = defaultClaudeOrchestratorSCMMaxResponseBytes
	}

	client := opts.HTTPClient
	if client == nil {
		// A zero-value Transport makes direct requests; DefaultTransport would
		// silently pick up HTTP_PROXY/HTTPS_PROXY from the process environment.
		client = &http.Client{Transport: &http.Transport{}}
	} else {
		clientCopy := *client
		client = &clientCopy
	}
	// The configured API is the only allowed destination. Reject all redirects
	// rather than risk forwarding credentials or replaying an external write.
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &ClaudeOrchestratorClient{
		baseURL:          base,
		httpClient:       client,
		timeout:          timeout,
		maxResponseBytes: maxResponseBytes,
		credential:       opts.Credential,
		writePolicy:      opts.WritePolicy,
	}, nil
}

// CreateBranch creates one branch after action-scoped approval succeeds.
func (c *ClaudeOrchestratorClient) CreateBranch(ctx context.Context, request ports.ClaudeOrchestratorSCMCreateBranchRequest) (ports.ClaudeOrchestratorSCMBranchResult, error) {
	var result ports.ClaudeOrchestratorSCMBranchResult
	if err := c.writePolicy.Authorize(request.Approval, ports.ClaudeOrchestratorSCMCreateBranch); err != nil {
		return result, err
	}
	if err := validateRepository(request.Repository); err != nil {
		return result, err
	}
	var response struct {
		BranchName string `json:"branch_name"`
		CommitSHA  string `json:"commit_sha"`
	}
	if err := c.doJSON(ctx, http.MethodPost, []string{"repos", request.Repository.Owner, request.Repository.Name, "branches"}, map[string]any{
		"branch_name": request.BranchName,
		"base_branch": request.BaseBranch,
		"base_sha":    request.BaseSHA,
	}, &response); err != nil {
		return result, err
	}
	result.BranchName = response.BranchName
	if result.BranchName == "" {
		result.BranchName = request.BranchName
	}
	result.CommitSHA = response.CommitSHA
	if result.CommitSHA == "" {
		result.CommitSHA = request.BaseSHA
	}
	return result, nil
}

// Push publishes an existing commit after independent action-scoped approval.
func (c *ClaudeOrchestratorClient) Push(ctx context.Context, request ports.ClaudeOrchestratorSCMPushRequest) (ports.ClaudeOrchestratorSCMPushResult, error) {
	var result ports.ClaudeOrchestratorSCMPushResult
	if err := c.writePolicy.Authorize(request.Approval, ports.ClaudeOrchestratorSCMPush); err != nil {
		return result, err
	}
	if err := validateRepository(request.Repository); err != nil {
		return result, err
	}
	var response struct {
		BranchName string `json:"branch_name"`
		CommitSHA  string `json:"commit_sha"`
	}
	if err := c.doJSON(ctx, http.MethodPost, []string{"repos", request.Repository.Owner, request.Repository.Name, "pushes"}, map[string]any{
		"branch_name": request.BranchName,
		"commit_sha":  request.CommitSHA,
	}, &response); err != nil {
		return result, err
	}
	result.BranchName = response.BranchName
	if result.BranchName == "" {
		result.BranchName = request.BranchName
	}
	result.CommitSHA = response.CommitSHA
	if result.CommitSHA == "" {
		result.CommitSHA = request.CommitSHA
	}
	return result, nil
}

// CreatePullRequest opens a pull request only after separate matching approval.
func (c *ClaudeOrchestratorClient) CreatePullRequest(ctx context.Context, request ports.ClaudeOrchestratorSCMCreatePullRequestRequest) (ports.ClaudeOrchestratorSCMCreatePullRequestResult, error) {
	var result ports.ClaudeOrchestratorSCMCreatePullRequestResult
	if err := c.writePolicy.Authorize(request.Approval, ports.ClaudeOrchestratorSCMCreatePR); err != nil {
		return result, err
	}
	if err := validateRepository(request.Repository); err != nil {
		return result, err
	}
	var response struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
	}
	if err := c.doJSON(ctx, http.MethodPost, []string{"repos", request.Repository.Owner, request.Repository.Name, "pull-requests"}, map[string]any{
		"head_branch": request.HeadBranch,
		"base_branch": request.BaseBranch,
		"title":       request.Title,
		"body":        request.Body,
		"draft":       request.Draft,
	}, &response); err != nil {
		return result, err
	}
	result.PR = ports.SCMPRRef{Repo: request.Repository, Number: response.Number, URL: response.URL}
	if response.Number < 1 {
		return ports.ClaudeOrchestratorSCMCreatePullRequestResult{}, errors.New("claude orchestrator SCM: response did not include a valid pull request number")
	}
	return result, nil
}

// GetChecks reads the normalized check snapshot for the requested PR head.
func (c *ClaudeOrchestratorClient) GetChecks(ctx context.Context, request ports.ClaudeOrchestratorSCMChecksRequest) (ports.ClaudeOrchestratorSCMChecksResult, error) {
	var result ports.ClaudeOrchestratorSCMChecksResult
	if err := validateRepository(request.PR.Repo); err != nil {
		return result, err
	}
	if request.PR.Number < 1 {
		return result, errors.New("claude orchestrator SCM: pull request number must be positive")
	}
	segments := []string{"repos", request.PR.Repo.Owner, request.PR.Repo.Name, "pull-requests", strconv.Itoa(request.PR.Number), "checks"}
	var response struct {
		HeadSHA string `json:"head_sha"`
		Checks  []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			URL        string `json:"url"`
			LogTail    string `json:"log_tail"`
			ProviderID string `json:"provider_id"`
		} `json:"checks"`
	}
	if err := c.doJSON(ctx, http.MethodGet, segments, nil, &response, url.Values{"head_sha": []string{request.HeadSHA}}); err != nil {
		return result, err
	}
	result.HeadSHA = response.HeadSHA
	if result.HeadSHA == "" {
		result.HeadSHA = request.HeadSHA
	}
	result.Checks = make([]ports.SCMCheckObservation, 0, len(response.Checks))
	for _, check := range response.Checks {
		result.Checks = append(result.Checks, ports.SCMCheckObservation{
			Name: check.Name, Status: check.Status, Conclusion: check.Conclusion,
			URL: check.URL, LogTail: check.LogTail, ProviderID: check.ProviderID,
		})
	}
	return result, nil
}

func validateRepository(repo ports.SCMRepo) error {
	if strings.TrimSpace(repo.Owner) == "" || strings.TrimSpace(repo.Name) == "" {
		return errors.New("claude orchestrator SCM: repository owner and name are required")
	}
	for _, segment := range []string{repo.Owner, repo.Name} {
		if segment == "." || segment == ".." || strings.ContainsAny(segment, "/\\\x00\r\n") {
			return errors.New("claude orchestrator SCM: invalid repository path segment")
		}
	}
	return nil
}

func (c *ClaudeOrchestratorClient) doJSON(ctx context.Context, method string, segments []string, input any, output any, query ...url.Values) error {
	if ctx == nil {
		return errors.New("claude orchestrator SCM: context must not be nil")
	}
	requestURL := *c.baseURL
	requestURL.Path = strings.TrimRight(requestURL.Path, "/")
	requestURL.RawPath = strings.TrimRight(requestURL.EscapedPath(), "/")
	for _, segment := range segments {
		requestURL.Path += "/" + segment
		requestURL.RawPath += "/" + url.PathEscape(segment)
	}
	if len(query) > 0 && len(query[0]) > 0 {
		requestURL.RawQuery = query[0].Encode()
	}

	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("claude orchestrator SCM: encode %s request: %w", method, err)
		}
		body = bytes.NewReader(encoded)
	}
	requestContext, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestContext, method, requestURL.String(), body)
	if err != nil {
		return errors.New("claude orchestrator SCM: could not build HTTP request")
	}
	req.Header.Set("Accept", "application/json")
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := c.credential.Value(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("claude orchestrator SCM: request deadline exceeded: %w", context.DeadlineExceeded)
		}
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("claude orchestrator SCM: request canceled: %w", context.Canceled)
		}
		return fmt.Errorf("claude orchestrator SCM: HTTP request failed: %s", c.redact(err.Error()))
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseBytes+1))
	if err != nil {
		return errors.New("claude orchestrator SCM: could not read HTTP response")
	}
	if int64(len(responseBody)) > c.maxResponseBytes {
		return fmt.Errorf("claude orchestrator SCM: response exceeds configured maximum of %d bytes", c.maxResponseBytes)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("claude orchestrator SCM: HTTP %d from configured API", resp.StatusCode)
	}
	if len(responseBody) == 0 || output == nil {
		return nil
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		// Do not include the raw decoder error or payload: either may contain
		// credentials echoed by a remote service.
		return errors.New("claude orchestrator SCM: invalid JSON response")
	}
	return nil
}

func (c *ClaudeOrchestratorClient) redact(message string) string {
	if token := c.credential.Value(); token != "" {
		return strings.ReplaceAll(message, token, "[REDACTED]")
	}
	return message
}
