import assert from "node:assert/strict";
import test from "node:test";
import { authorizeCommand, authorizeFileAccess, isSensitivePath, type TaskSecurityPolicy } from "./security-policy.ts";

const policy: TaskSecurityPolicy = {
  taskId: "task-a",
  readPaths: ["src/**", "README.md"],
  writePaths: ["src/feature.ts", "test/"],
  allowedCommands: ["node", "git", "npm", "echo"],
};

test("sensitive paths are denied even when a task rule would otherwise allow access", () => {
  const broadPolicy: TaskSecurityPolicy = { ...policy, readPaths: ["src/**"], writePaths: ["src/**"] };
  for (const path of [
    ".env", ".ENV.production", ".envrc", "src/.env.local", "src/id_ed25519",
    "src/private.pem", "src/signing-key.p12", "src/.aws/credentials", "src/secrets.yaml",
    "src/service-account-prod.json", "src/state.tfstate",
  ]) {
    assert.equal(isSensitivePath(path), true, `${path} should be sensitive`);
    assert.equal(authorizeFileAccess(broadPolicy, "task-a", "read", path).code, "sensitive_path");
    assert.equal(authorizeFileAccess(broadPolicy, "task-a", "write", path).code, "sensitive_path");
  }
});

test("read and write rights are separate and scoped to the requested task", () => {
  assert.equal(authorizeFileAccess(policy, "task-a", "read", "src/lib.ts").allowed, true);
  assert.equal(authorizeFileAccess(policy, "task-a", "read", "README.md").allowed, true);
  assert.equal(authorizeFileAccess(policy, "task-a", "write", "src/feature.ts").allowed, true);
  assert.equal(authorizeFileAccess(policy, "task-a", "write", "src/lib.ts").code, "write_not_permitted");
  assert.equal(authorizeFileAccess(policy, "task-a", "read", "test/new.test.ts").code, "read_not_permitted");
  assert.equal(authorizeFileAccess(policy, "another-task", "read", "src/lib.ts").code, "task_mismatch");
});

test("path checks reject traversal, absolute paths, drive paths, and NUL bytes", () => {
  for (const path of ["../src/feature.ts", "src/../../etc/passwd", "/workspace/src/a.ts", "C:\\secret.txt", "src/\0x"]) {
    assert.equal(authorizeFileAccess(policy, "task-a", "read", path).allowed, false, path);
    assert.equal(isSensitivePath(path), true, path);
  }
  assert.equal(authorizeFileAccess(policy, "task-a", "read", "src/./lib.ts").allowed, true);
  assert.equal(authorizeFileAccess({ ...policy, readPaths: ["../**"] }, "task-a", "read", "src/a.ts").code, "invalid_policy");
});

test("commands require an allowlisted bare executable and are passed as argv", () => {
  assert.equal(authorizeCommand(policy, "task-a", ["node", "--test", "src/example.test.ts"]).allowed, true);
  assert.equal(authorizeCommand(policy, "task-a", ["curl", "https://example.invalid"]).code, "command_denied");
  assert.equal(authorizeCommand(policy, "task-a", ["python", "tool.py"]).code, "command_not_allowlisted");
  assert.equal(authorizeCommand(policy, "task-a", ["/bin/echo", "hello"]).code, "invalid_command");
  assert.equal(authorizeCommand(policy, "task-a", []).code, "invalid_command");
  assert.equal(authorizeCommand(policy, "task-a", ["node", "--eval", "process.exit(1)"]).code, "unsafe_command_arguments");
  assert.equal(authorizeCommand(policy, "task-a", ["npm", "exec", "--", "other-tool"]).code, "unsafe_command_arguments");
  assert.equal(authorizeCommand(policy, "task-a", ["git", "reset", "--hard"]).code, "unsafe_command_arguments");
});

test("task-level command denies take precedence over the allowlist", () => {
  const restrictive = { ...policy, deniedCommands: ["echo"] };
  assert.equal(authorizeCommand(restrictive, "task-a", ["echo", "hello"]).code, "command_denied");
  assert.equal(authorizeCommand(policy, "wrong-task", ["node", "--version"]).code, "task_mismatch");
});
