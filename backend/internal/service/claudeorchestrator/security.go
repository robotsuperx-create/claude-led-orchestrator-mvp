// Package claudeorchestrator contains security helpers for orchestrator inputs and logs.
package claudeorchestrator

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const redactedValue = "[REDACTED]"

var (
	// sensitiveAssignment matches common .env, configuration, and structured-log
	// assignments without depending on a particular provider's variable names.
	sensitiveAssignment = regexp.MustCompile(`(?i)(\b(?:[A-Za-z_][A-Za-z0-9_.-]*[_-])?(?:API[_-]?KEY|ACCESS[_-]?KEY|SECRET(?:[_-]?(?:KEY|TOKEN))?|PRIVATE[_-]?KEY|CLIENT[_-]?SECRET|PASSWORD|PASSWD|TOKEN|AUTH[_-]?TOKEN|CREDENTIALS?)(?:[_-]?[A-Za-z0-9_.-]*)?\b\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;]+)`)

	// authorizationAssignment handles header and .env forms, preserving a
	// Bearer/Basic scheme while replacing the credential itself.
	authorizationAssignment = regexp.MustCompile(`(?i)(\bauthorization\b\s*[:=]\s*)((?:bearer|basic)\s+)?(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;]+)`)

	// knownAPIKey matches recognizable provider/API-key formats even when a
	// preceding variable name or Authorization header was omitted from a log.
	knownAPIKey = regexp.MustCompile(`(?i)\b(?:sk-(?:ant-)?[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9-]{10,}|AIza[A-Za-z0-9_-]{30,}|AKIA[0-9A-Z]{16})\b`)
)

// RedactSecrets removes recognizable API credentials and secret assignments
// from arbitrary text, including log lines and .env-style KEY=value entries.
// It is best-effort: callers must not treat redaction as a substitute for
// avoiding secret-bearing logs or protecting the resulting data.
func RedactSecrets(text string) string {
	text = authorizationAssignment.ReplaceAllString(text, "${1}${2}"+redactedValue)
	text = sensitiveAssignment.ReplaceAllString(text, "${1}"+redactedValue)
	return knownAPIKey.ReplaceAllString(text, redactedValue)
}

// deniedCommandNames is an intentionally explicit denylist of common shell or
// command-launching interpreters, privilege-escalation tools, destructive
// utilities, and system-management commands. It is not an exhaustive security
// boundary; see ValidateCommand's warning.
var deniedCommandNames = map[string]struct{}{
	// Shells and command interpreters.
	"sh": {}, "bash": {}, "dash": {}, "zsh": {}, "fish": {}, "csh": {}, "tcsh": {}, "ksh": {},
	"cmd": {}, "powershell": {}, "pwsh": {},
	"python": {}, "python2": {}, "python3": {}, "perl": {}, "ruby": {}, "node": {}, "php": {}, "lua": {},
	// Tools that commonly act as indirect command launchers.
	"env": {}, "xargs": {}, "busybox": {}, "find": {},
	// Privilege escalation.
	"sudo": {}, "su": {}, "doas": {}, "pkexec": {}, "runuser": {},
	// Destructive or host/system administration commands.
	"rm": {}, "rmdir": {}, "shred": {}, "dd": {}, "fdisk": {}, "parted": {}, "wipefs": {},
	"shutdown": {}, "reboot": {}, "halt": {}, "poweroff": {}, "init": {},
	"mount": {}, "umount": {}, "chroot": {}, "chmod": {}, "chown": {}, "chattr": {},
	"systemctl": {}, "service": {},
}

// ValidateCommand applies a conservative denylist to an argv vector. It never
// parses, joins, or interprets shell syntax: callers must execute argv directly
// (for example with exec.CommandContext(argv[0], argv[1:]...)), never through a
// shell. A denied executable is identified by its basename, including paths
// with either slash convention and a Windows .exe suffix.
//
// WARNING: this policy is not a sandbox. A non-denied program may still perform
// dangerous actions, invoke other programs, or exploit the host. Use OS-level
// isolation and least privilege for untrusted commands.
func ValidateCommand(argv []string) error {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return fmt.Errorf("command argv must include a non-empty executable")
	}
	for i, arg := range argv {
		if strings.IndexByte(arg, 0) >= 0 {
			return fmt.Errorf("command argv[%d] contains a NUL byte", i)
		}
	}

	// filepath.Base alone does not recognize Windows separators on Unix.
	command := strings.ReplaceAll(argv[0], `\`, "/")
	name := strings.ToLower(filepath.Base(command))
	name = strings.TrimSuffix(name, ".exe")
	if _, denied := deniedCommandNames[name]; denied {
		return fmt.Errorf("command %q is denied by the orchestrator command policy", name)
	}
	// Block versioned Python executable names (python3.11, python2.7, etc.).
	if strings.HasPrefix(name, "python") && len(name) > len("python") {
		suffix := name[len("python"):]
		if strings.Trim(suffix, "0123456789.") == "" {
			return fmt.Errorf("command %q is denied by the orchestrator command policy", name)
		}
	}
	return nil
}
