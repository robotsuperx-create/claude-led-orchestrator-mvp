import assert from "node:assert/strict";
import test from "node:test";
import { ContextManager } from "./context-manager.ts";

const manager = new ContextManager({ tokenBudget: 100 });

test("includes only allowlisted summaries and diffs", () => {
  const result = manager.build({
    task: "Fix the parser.",
    allowedFiles: ["src/parser.ts", "src/types.ts"],
    files: [
      {
        path: "src/parser.ts",
        summary: "Parses the input stream.",
        diff: "+ handles escaped strings",
      },
      { path: "src/types.ts", summary: "Token is represented as a string." },
      { path: "private/key.ts", summary: "this path is not allowed" },
    ],
  });

  assert.match(result.context, /Summary src\/parser\.ts/);
  assert.match(result.context, /Diff src\/parser\.ts/);
  assert.match(result.context, /Summary src\/types\.ts/);
  assert.doesNotMatch(result.context, /private\/key/);
  assert.deepEqual(result.includedFiles, ["src/parser.ts", "src/types.ts"]);
  assert.deepEqual(result.omittedFiles, []);
  assert.equal(result.estimatedTokens <= result.tokenBudget, true);
});

test("uses only a diff when available and omits files without summaries/diffs or not on the allowlist", () => {
  const result = manager.build({
    task: "Review changes.",
    allowedFiles: ["src/a.ts", "src/no-context.ts", "src/missing.ts"],
    files: [
      { path: "src/a.ts", diff: "@@ -1 +1 @@\n-old\n+new" },
      { path: "src/no-context.ts" },
      { path: "src/other.ts", summary: "untrusted/unrequested" },
    ],
  });

  assert.match(result.context, /Diff src\/a\.ts/);
  assert.doesNotMatch(result.context, /untrusted\/unrequested/);
  assert.deepEqual(result.includedSections, [{ path: "src/a.ts", kind: "diff" }]);
  assert.deepEqual(result.omittedFiles, ["src/no-context.ts", "src/missing.ts"]);
});

test("fits whole sections into the token budget and truncates an oversized task safely", () => {
  const small = new ContextManager({ tokenBudget: 8 });
  const result = small.build({
    task: "A task prompt that is much longer than the tiny budget allows.",
    allowedFiles: ["a.ts"],
    files: [{ path: "a.ts", summary: "A long summary that will not fit." }],
  });

  assert.equal(result.taskTruncated, true);
  assert.equal(result.estimatedTokens <= 8, true);
  assert.deepEqual(result.includedFiles, []);
  assert.deepEqual(result.omittedFiles, ["a.ts"]);
});

test("rejects malformed inputs and invalid token estimators", () => {
  assert.throws(
    () => manager.build({ task: "ok", allowedFiles: [" "], files: [] }),
    /allowedFiles/,
  );
  assert.throws(
    () => manager.build({ task: "ok", allowedFiles: ["a"], files: [{ path: "a" }, { path: "a" }] }),
    /duplicate file path/,
  );
  assert.throws(
    () => new ContextManager({ estimateTokens: () => -1 }).build({
      task: "ok",
      allowedFiles: [],
      files: [],
    }),
    /non-negative integer/,
  );
});
