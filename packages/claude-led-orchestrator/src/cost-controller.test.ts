import assert from "node:assert/strict";
import test from "node:test";
import { CostController } from "./cost-controller.ts";

test("tracks input/output tokens and caller-reported costs cumulatively", () => {
  const controller = new CostController();
  assert.deepEqual(
    controller.recordUsage({ requestId: "r1", inputTokens: 10, outputTokens: 4, costUsd: 0.002, model: "local-test" }),
    { requestCount: 1, inputTokens: 10, outputTokens: 4, totalTokens: 14, costUsd: 0.002 },
  );
  controller.recordUsage({ requestId: "r2", inputTokens: 3, outputTokens: 5, costUsd: 0.001 });
  assert.deepEqual(controller.getTotals(), {
    requestCount: 2,
    inputTokens: 13,
    outputTokens: 9,
    totalTokens: 22,
    costUsd: 0.003,
  });

  const records = controller.getRecords();
  assert.equal(records.length, 2);
  assert.throws(() => controller.recordUsage({ requestId: "r1", inputTokens: 1, outputTokens: 1, costUsd: 0 }), /duplicate requestId/);
});

test("retries configured failures, then escalates after the retry limit", () => {
  const controller = new CostController({ maxRetries: 2, retryOn: ["timeout"] });
  assert.deepEqual(controller.decideOnFailure({ attempt: 1, kind: "timeout" }), {
    action: "retry",
    attempt: 1,
    kind: "timeout",
    reason: "retryable_failure",
    retriesRemaining: 1,
    exceededBudgets: [],
  });
  assert.equal(controller.decideOnFailure({ attempt: 2, kind: "timeout" }).action, "retry");
  const exhausted = controller.decideOnFailure({ attempt: 3, kind: "timeout", message: "still failing" });
  assert.equal(exhausted.action, "escalate");
  assert.equal(exhausted.reason, "retries_exhausted");
  assert.equal(exhausted.retriesRemaining, 0);
  assert.equal(exhausted.message, "still failing");
});

test("escalates immediately for non-retryable failures or exceeded budgets", () => {
  const controller = new CostController({ maxTotalTokens: 10, maxCostUsd: 0.01 });
  assert.equal(controller.decideOnFailure({ attempt: 1, kind: "permission_denied" }).reason, "non_retryable_failure");
  controller.recordUsage({ requestId: "over-budget", inputTokens: 8, outputTokens: 3, costUsd: 0.02 });
  assert.equal(controller.isBudgetExceeded(), true);
  assert.deepEqual(controller.getExceededBudgets(), ["tokens", "cost"]);
  const decision = controller.decideOnFailure({ attempt: 1, kind: "timeout" });
  assert.equal(decision.action, "escalate");
  assert.equal(decision.reason, "budget_exceeded");
  assert.deepEqual(decision.exceededBudgets, ["tokens", "cost"]);
});

test("validates usage data, options, and failure attempt numbers", () => {
  assert.throws(() => new CostController({ maxRetries: -1 }), /maxRetries/);
  assert.throws(() => new CostController({ maxCostUsd: Number.NaN }), /maxCostUsd/);
  const controller = new CostController();
  assert.throws(() => controller.recordUsage({ requestId: "bad", inputTokens: -1, outputTokens: 0, costUsd: 0 }), /inputTokens/);
  assert.throws(() => controller.recordUsage({ requestId: "bad", inputTokens: 1, outputTokens: 0, costUsd: -1 }), /costUsd/);
  assert.throws(() => controller.decideOnFailure({ attempt: 0, kind: "timeout" }), /at least 1/);
});
