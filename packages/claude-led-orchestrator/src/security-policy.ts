/**
 * Conservative, synchronous policy checks for orchestrator tasks.
 * This module authorizes intent; it does not perform filesystem or process isolation.
 */

export interface TaskSecurityPolicy {
  /** Policy owner; every decision must name the same task. */
  readonly taskId: string;
  /** Relative paths allowed for reads. A trailing slash or `/**` grants a directory subtree. */
  readonly readPaths: readonly string[];
  /** Relative paths allowed for writes. A trailing slash or `/**` grants a directory subtree. */
  readonly writePaths: readonly string[];
  /** Executable names allowed for this task. Commands must be supplied as argv, never shell text. */
  readonly allowedCommands: readonly string[];
  /** Additional executable names to deny; built-in deny rules always apply. */
  readonly deniedCommands?: readonly string[];
}

export type SecurityDecisionCode =
  | "allowed"
  | "invalid_policy"
  | "task_mismatch"
  | "invalid_path"
  | "sensitive_path"
  | "read_not_permitted"
  | "write_not_permitted"
  | "invalid_command"
  | "command_not_allowlisted"
  | "command_denied"
  | "unsafe_command_arguments";

export interface SecurityDecision {
  readonly allowed: boolean;
  readonly code: SecurityDecisionCode;
  readonly reason: string;
}

export type FileAccess = "read" | "write";

const BUILTIN_DENIED_COMMANDS = new Set([
  "at", "bash", "csh", "cmd", "command", "crontab", "dash", "dd", "doas", "eval",
  "fdisk", "fish", "ftp", "kill", "killall", "launchctl", "mkfs", "mount", "nc",
  "ncat", "netcat", "powershell", "printenv", "pkill", "pwsh", "reboot", "rm", "rmdir",
  "scp", "sftp", "sh", "shred", "shutdown", "socat", "source", "ssh", "su", "sudo",
  "systemctl", "tcsh", "telnet", "umount", "unlink", "wget", "curl", "chmod", "chown",
  "chgrp", "env",
]);

function decision(code: SecurityDecisionCode, reason: string): SecurityDecision {
  return { allowed: code === "allowed", code, reason };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function normalizeRelativePath(value: unknown): string | null {
  if (typeof value !== "string" || value.trim().length === 0 || value.includes("\0")) return null;
  // Normalize Windows separators before checking roots and traversal so policy behavior is
  // consistent if a caller runs on a different platform.
  const path = value.replace(/\\/g, "/");
  if (path.startsWith("/") || /^[a-zA-Z]:/.test(path)) return null;
  const parts = path.split("/");
  if (parts.some((part) => part === "..")) return null;
  const normalized = parts.filter((part) => part !== "" && part !== ".").join("/");
  return normalized.length > 0 ? normalized : null;
}

function isSensitiveNormalizedPath(path: string): boolean {
  const segments = path.split("/").map((segment) => segment.toLowerCase());
  for (const segment of segments) {
    if (
      segment === ".env" || segment.startsWith(".env.") || segment.startsWith(".env-") ||
      segment === ".envrc" || segment.startsWith(".envrc.")
    ) return true;

    if ([".ssh", ".aws", ".azure", ".gnupg", ".kube", ".gcloud", ".vault"].includes(segment)) {
      return true;
    }
    if (
      segment === ".npmrc" || segment === ".pypirc" || segment === ".netrc" ||
      segment === ".git-credentials" || segment === "credentials" || segment === ".credentials" ||
      segment === "secrets" || segment === ".secrets" || segment === "config.json" &&
        segments.includes(".docker")
    ) return true;

    // Common private-key and secret-bearing filenames / formats. Public certificates are
    // intentionally not broadly blocked, but filenames that identify an SSH key are.
    if (/^id_(rsa|dsa|ecdsa|ed25519)(?:$|[._-])/.test(segment)) return true;
    if (/\.(pem|key|p12|pfx|jks|keystore|priv|private)$/.test(segment)) return true;
    if (/\.(tfstate|tfstate\.backup)$/.test(segment)) return true;
    if (/^(service[-_.]?account|firebase-adminsdk)(?:[-_.].*)?\.json$/.test(segment)) return true;
    if (/(?:^|[._-])(secret|secrets|credential|credentials|password|passwd|token|api[-_]?key|private[-_]?key)(?:$|[._-])/.test(segment)) {
      return true;
    }
  }
  return false;
}

/** Return true for known sensitive paths; malformed, absolute, or traversing paths fail closed. */
export function isSensitivePath(path: string): boolean {
  const normalized = normalizeRelativePath(path);
  return normalized === null || isSensitiveNormalizedPath(normalized);
}

interface PathRule {
  readonly path: string;
  readonly subtree: boolean;
}

function parsePathRule(value: unknown): PathRule | null {
  if (typeof value !== "string" || value.trim().length === 0) return null;
  const subtree = value.endsWith("/") || value.endsWith("/**");
  const rawBase = value.endsWith("/**") ? value.slice(0, -3) : subtree ? value.slice(0, -1) : value;
  const normalized = normalizeRelativePath(rawBase);
  if (normalized === null || isSensitiveNormalizedPath(normalized)) return null;
  return { path: normalized, subtree };
}

function isCommandName(value: unknown): value is string {
  return typeof value === "string" && /^[a-zA-Z0-9][a-zA-Z0-9._+-]*$/.test(value);
}

function isValidPolicy(policy: unknown): policy is TaskSecurityPolicy {
  if (!isRecord(policy) || typeof policy.taskId !== "string" || policy.taskId.trim().length === 0) return false;
  if (!Array.isArray(policy.readPaths) || !policy.readPaths.every((entry) => parsePathRule(entry) !== null)) return false;
  if (!Array.isArray(policy.writePaths) || !policy.writePaths.every((entry) => parsePathRule(entry) !== null)) return false;
  if (!Array.isArray(policy.allowedCommands) || !policy.allowedCommands.every(isCommandName)) return false;
  if (policy.deniedCommands !== undefined &&
      (!Array.isArray(policy.deniedCommands) || !policy.deniedCommands.every(isCommandName))) return false;
  return true;
}

function pathMatches(rule: PathRule, path: string): boolean {
  return path === rule.path || (rule.subtree && path.startsWith(`${rule.path}/`));
}

/** Authorize a relative file path for a specific task and operation. */
export function authorizeFileAccess(
  policy: TaskSecurityPolicy,
  taskId: string,
  access: FileAccess,
  path: string,
): SecurityDecision {
  if (!isValidPolicy(policy)) return decision("invalid_policy", "Task security policy is malformed or contains an unsafe path rule.");
  if (taskId !== policy.taskId) return decision("task_mismatch", "The request task does not own this policy.");
  if (access !== "read" && access !== "write") return decision("invalid_policy", "Access must be read or write.");

  const normalized = normalizeRelativePath(path);
  if (normalized === null) return decision("invalid_path", "Path must be a non-empty relative path without traversal.");
  if (isSensitiveNormalizedPath(normalized)) return decision("sensitive_path", "Sensitive paths are denied regardless of task permissions.");

  const rules = (access === "read" ? policy.readPaths : policy.writePaths)
    .map(parsePathRule)
    .filter((rule): rule is PathRule => rule !== null);
  if (rules.some((rule) => pathMatches(rule, normalized))) return decision("allowed", "Path is within this task's explicit permission.");
  return decision(access === "read" ? "read_not_permitted" : "write_not_permitted", `Task has no ${access} permission for this path.`);
}

function hasUnsafeCommandShape(argv: readonly string[]): boolean {
  if (argv.some((part) => typeof part !== "string" || part.length === 0 || part.includes("\0") || /[\r\n]/.test(part))) {
    return true;
  }
  const executable = argv[0]?.toLowerCase();
  if (executable === undefined) return true;
  const args = argv.slice(1).map((arg) => arg.toLowerCase());

  // Do not allow common interpreter escape hatches or package-manager dispatch of arbitrary scripts.
  if (executable === "node" && args.some((arg) => ["-e", "--eval", "-r", "--require", "--import", "--loader"].includes(arg))) {
    return true;
  }
  const firstArgument = args[0];
  if (["npm", "pnpm", "yarn"].includes(executable) && firstArgument !== undefined &&
      ["exec", "dlx", "run", "run-script", "install", "i", "ci"].includes(firstArgument)) {
    return true;
  }
  if (executable === "git" && (
    args[0] === "push" || args[0] === "clean" ||
    args[0] === "reset" && args.includes("--hard") ||
    args[0] === "submodule" || args[0] === "-c" ||
    args[0] === "config" && args.some((arg) => arg === "--global" || arg === "--system")
  )) return true;
  return false;
}

/**
 * Validate a command represented as an argv vector (not a shell command string). Callers must
 * execute an allowed vector with shell=false; quoting/splitting command text is not supported.
 */
export function authorizeCommand(
  policy: TaskSecurityPolicy,
  taskId: string,
  argv: readonly string[],
): SecurityDecision {
  if (!isValidPolicy(policy)) return decision("invalid_policy", "Task security policy is malformed.");
  if (taskId !== policy.taskId) return decision("task_mismatch", "The request task does not own this policy.");
  if (!Array.isArray(argv) || argv.length === 0 || !isCommandName(argv[0]) ||
      argv.some((part) => typeof part !== "string")) {
    return decision("invalid_command", "Command must be a non-empty argv vector with a bare executable name.");
  }

  const executable = argv[0].toLowerCase();
  const taskDenied = (policy.deniedCommands ?? []).some((command) => command.toLowerCase() === executable);
  if (BUILTIN_DENIED_COMMANDS.has(executable) || taskDenied) {
    return decision("command_denied", `Executable '${executable}' is explicitly denied.`);
  }
  if (!policy.allowedCommands.some((command) => command.toLowerCase() === executable)) {
    return decision("command_not_allowlisted", `Executable '${executable}' is not allowlisted for this task.`);
  }
  if (hasUnsafeCommandShape(argv)) {
    return decision("unsafe_command_arguments", "The command uses a denied interpreter, package-manager, or git operation.");
  }
  return decision("allowed", "Executable is allowlisted and the command passed built-in deny checks.");
}
