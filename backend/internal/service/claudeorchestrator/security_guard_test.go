package claudeorchestrator

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestTheSecurityGuardBlocksSecretsAndFlagsRiskyCode(t *testing.T) {
	diff := strings.Join([]string{
		"+++ b/src/config.ts",
		"@@ -0,0 +10,6 @@",
		`+const key = "sk-ant-api03-` + strings.Repeat("a", 40) + `";`,
		`+const token = process.env.API_TOKEN;`,
		`+API_KEY = "your_api_key_here"`,
		`+const agent = new https.Agent({ rejectUnauthorized: false });`,
		`+el.innerHTML = userText;`,
		`+db.query("SELECT * FROM users WHERE id = " + id);`,
		"+++ b/keys/id_rsa",
		"@@ -0,0 +1 @@",
		"+-----BEGIN OPENSSH PRIVATE KEY-----",
	}, "\n")
	critical, warnings := guardDiff(diff)
	if strings.Join(critical, "|") != "src/config.ts:10: a secret written into the code|src/config.ts:13: TLS certificate checking switched off|keys/id_rsa:1: a private key" {
		t.Fatalf("critical = %q", critical)
	}
	if strings.Join(warnings, "|") != "src/config.ts:14: HTML written from a string (cross-site scripting risk)|src/config.ts:15: SQL built by joining strings (SQL injection risk)" {
		t.Fatalf("warnings = %q", warnings)
	}
	if strings.Contains(strings.Join(critical, " "), "sk-ant") {
		t.Fatal("a finding repeated the secret")
	}
}

func TestTheGuardHoldsTheRunOnlyForBlockingFindings(t *testing.T) {
	clean, _ := guardDiff("+++ b/app.js\n@@ -0,0 +1 @@\n+console.log('hello');")
	if len(clean) != 0 {
		t.Fatalf("clean code flagged: %q", clean)
	}
	report := ports.ValidationReport{Passed: true}
	service := &Service{workspaces: diffWorkspaces{diff: "+++ b/a.go\n@@ -0,0 +1 @@\n+tls := &tls.Config{InsecureSkipVerify: true}"}}
	service.guard(t.Context(), ports.RunWorkspace{Path: t.TempDir()}, &report)
	if report.Passed || len(report.Issues) != 1 || !strings.HasPrefix(report.Issues[0], "Security guard (blocking): a.go:1") {
		t.Fatalf("report = %+v", report)
	}
}

type diffWorkspaces struct {
	ports.RunWorkspaceProvisioner
	diff string
}

func (d diffWorkspaces) FullDiff(context.Context, ports.RunWorkspace) (string, error) { return d.diff, nil }
