package claudeorchestrator

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The security guard reads everything a run added before Claude reviews it:
// a secret, a private key or switched-off TLS checking fails validation, so
// the run is held; risky patterns are reported for the review to weigh.
// Findings name the file and line, never the secret itself.

const (
	maxGuardDiffBytes = 4 << 20
	maxGuardFindings  = 20
)

// RunWorkspaceDiff gives the whole of a run's change (tracked and new files).
type RunWorkspaceDiff interface {
	FullDiff(ctx context.Context, workspace ports.RunWorkspace) (string, error)
}

type guardRule struct {
	pattern  *regexp.Regexp
	what     string
	critical bool
}

var guardRules = []guardRule{
	{regexp.MustCompile(`-{5}BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-{5}`), "a private key", true},
	{regexp.MustCompile(`(?i)\bInsecureSkipVerify\s*:\s*true\b|\brejectUnauthorized\s*:\s*false\b|\bverify\s*=\s*False\b|NODE_TLS_REJECT_UNAUTHORIZED\s*=\s*['"]?0`), "TLS certificate checking switched off", true},
	{regexp.MustCompile(`\beval\s*\(|\bnew\s+Function\s*\(`), "code built from text at run time (eval)", false},
	{regexp.MustCompile(`dangerouslySetInnerHTML|\.innerHTML\s*=|\.outerHTML\s*=|document\.write\s*\(`), "HTML written from a string (cross-site scripting risk)", false},
	{regexp.MustCompile(`(?i)\b(?:exec|execSync|spawn)\s*\(\s*` + "`" + `[^` + "`" + `]*\$\{|\bshell\s*:\s*true\b|\bsubprocess\.[a-z_]+\([^)]*shell\s*=\s*True`), "a shell command built from values (command injection risk)", false},
	{regexp.MustCompile(`(?i)["'` + "`" + `]\s*(?:SELECT|INSERT|UPDATE|DELETE)\b[^"'` + "`" + `]*["'` + "`" + `]?\s*\+|(?:SELECT|INSERT|UPDATE|DELETE)\b[^` + "`" + `\n]*\$\{`), "SQL built by joining strings (SQL injection risk)", false},
	{regexp.MustCompile(`(?i)\bchmod\s+(?:-R\s+)?777\b|\bos\.Chmod\([^)]*0o?777\)`), "a file opened to everyone (chmod 777)", false},
}

// placeholderValue is a value the crew wrote as an example, not a secret.
var placeholderValue = regexp.MustCompile(`(?i)(?:your|example|placeholder|changeme|xxx+|dummy|test|fake|sample|<[^>]*>|\$\{|process\.env|os\.getenv|os\.environ|import\.meta\.env|getenv)`)

// guardDiff scans the lines a diff adds.
func guardDiff(diff string) (critical, warnings []string) {
	file, line := "", 0
	add := func(list *[]string, finding string) {
		if len(critical)+len(warnings) < maxGuardFindings {
			*list = append(*list, finding)
		}
	}
	for _, raw := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(raw, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(raw, "+++ "), "b/")
			continue
		case strings.HasPrefix(raw, "@@"):
			// @@ -a,b +c,d @@: the added side starts at c.
			if _, err := fmt.Sscanf(raw[strings.Index(raw, "+")+1:], "%d", &line); err != nil {
				line = 0
			}
			continue
		case !strings.HasPrefix(raw, "+"):
			continue
		}
		text := strings.TrimPrefix(raw, "+")
		where := fmt.Sprintf("%s:%d", file, line)
		line++
		if RedactSecrets(text) != text && looksReal(text) {
			add(&critical, where+": a secret written into the code")
			continue
		}
		for _, rule := range guardRules {
			if rule.pattern.MatchString(text) {
				if rule.critical {
					add(&critical, where+": "+rule.what)
				} else {
					add(&warnings, where+": "+rule.what)
				}
				break
			}
		}
	}
	return critical, warnings
}

// looksReal is false for an assignment whose value is an example or comes
// from the environment.
func looksReal(text string) bool {
	if knownAPIKey.MatchString(text) {
		return true
	}
	match := sensitiveAssignment.FindString(text)
	if match == "" {
		return false
	}
	value := match[strings.IndexAny(match, ":=")+1:]
	value = strings.Trim(strings.TrimSpace(value), `"'`)
	return len(value) >= 12 && !placeholderValue.MatchString(value)
}

// guard runs the security guard over the run's change and adds what it found
// to the validation report.
func (s *Service) guard(ctx context.Context, workspace ports.RunWorkspace, report *ports.ValidationReport) {
	differ, ok := s.workspaces.(RunWorkspaceDiff)
	if !ok || workspace.Path == "" {
		return
	}
	diff, err := differ.FullDiff(ctx, workspace)
	if err != nil {
		return
	}
	critical, warnings := guardDiff(diff)
	for _, finding := range critical {
		report.Issues = append(report.Issues, "Security guard (blocking): "+finding)
	}
	for _, finding := range warnings {
		report.Issues = append(report.Issues, "Security guard (check): "+finding)
	}
	if len(critical) > 0 {
		report.Passed = false
	}
}
