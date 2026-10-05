package claudeorchestrator

import (
	"strings"
	"testing"
)

func TestRedactSecretsAssignmentsAndAuthorization(t *testing.T) {
	input := strings.Join([]string{
		`ANTHROPIC_API_KEY="anthropic-secret with spaces"`,
		`Authorization: Bearer header-secret`,
		`password = 'dotenv secret'`,
		`ordinary setting = visible`,
	}, "\n")

	got := RedactSecrets(input)
	for _, secret := range []string{"anthropic-secret", "header-secret", "dotenv secret"} {
		if strings.Contains(got, secret) {
			t.Errorf("RedactSecrets() leaked %q in %q", secret, got)
		}
	}
	if !strings.Contains(got, `ANTHROPIC_API_KEY=[REDACTED]`) {
		t.Errorf("API-key assignment was not redacted as expected: %q", got)
	}
	if !strings.Contains(got, "Authorization: Bearer [REDACTED]") {
		t.Errorf("Authorization scheme or redaction marker missing: %q", got)
	}
	if !strings.Contains(got, "ordinary setting = visible") {
		t.Errorf("non-secret setting was unexpectedly changed: %q", got)
	}
}

func TestRedactSecretsRecognizesStandaloneProviderKeysAndLogs(t *testing.T) {
	input := "request failed key=sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789 " +
		"github=ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	got := RedactSecrets(input)
	for _, secret := range []string{"sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789", "ghp_abcdefghijklmnopqrstuvwxyz0123456789"} {
		if strings.Contains(got, secret) {
			t.Errorf("RedactSecrets() leaked provider credential %q in %q", secret, got)
		}
	}
	if strings.Count(got, redactedValue) < 2 {
		t.Errorf("expected both provider credentials to be redacted, got %q", got)
	}
}

func TestValidateCommandUsesArgvAndDoesNotParseShellSyntax(t *testing.T) {
	// Shell metacharacters are ordinary arguments here. The caller must pass
	// argv directly to the OS process API rather than rejoining it for a shell.
	if err := ValidateCommand([]string{"echo", "literal ; rm -rf /", "&&", "not-a-pipeline"}); err != nil {
		t.Fatalf("literal argv arguments should be accepted: %v", err)
	}
}

func TestValidateCommandRejectsDenylistedExecutables(t *testing.T) {
	for _, argv := range [][]string{
		{"bash", "-c", "echo unsafe"},
		{"/usr/bin/sudo", "whoami"},
		{`C:\Windows\System32\PowerShell.exe`, "-Command", "..."},
		{"python3.11", "-c", "..."},
		{"rm", "-rf", "/"},
	} {
		if err := ValidateCommand(argv); err == nil {
			t.Errorf("ValidateCommand(%q) unexpectedly allowed a denied executable", argv[0])
		}
	}
}

func TestValidateCommandRejectsInvalidArgv(t *testing.T) {
	for _, argv := range [][]string{nil, {}, {""}, {" \t"}, {"echo", "contains\x00nul"}} {
		if err := ValidateCommand(argv); err == nil {
			t.Errorf("ValidateCommand(%q) expected an error", argv)
		}
	}
}
