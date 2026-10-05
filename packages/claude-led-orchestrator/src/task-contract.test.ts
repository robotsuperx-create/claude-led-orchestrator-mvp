import assert from "node:assert/strict";
import test from "node:test";
import {
  isAcceptanceCriterion,
  isAgentResult,
  isDiffSummary,
  isEscalationPolicy,
  isTaskContract,
  isValidationCommand,
  validateTaskContract,
} from "./task-contract.ts";

const criterion = {
  id: "ac-1",
  description: "The result includes a concise summary.",
  required: true,
};

const validationCommand = {
  id: "check-1",
  command: "npm test",
  required: true,
  timeoutMs: 30_000,
};

const escalationPolicy = {
  maxRetries: 2,
  triggers: ["agent_failure", "validation_failure"],
  escalateTo: "human-review",
} as const;

const diffSummary = {
  filesChanged: ["src/example.ts"],
  additions: 12,
  deletions: 3,
  summary: "Implemented the example change.",
};

const taskContract = {
  id: "task-1",
  title: "Implement a feature",
  description: "Implement and validate the requested feature.",
  acceptanceCriteria: [criterion],
  validationCommands: [validationCommand],
  escalationPolicy,
};

const agentResult = {
  agentId: "agent-1",
  status: "completed",
  summary: "The task is complete.",
  diffSummary,
  completedCriterionIds: ["ac-1"],
  failedCriterionIds: [],
};

test("accepts well-formed contract component values", () => {
  assert.equal(isAcceptanceCriterion(criterion), true);
  assert.equal(isValidationCommand(validationCommand), true);
  assert.equal(isEscalationPolicy(escalationPolicy), true);
  assert.equal(isDiffSummary(diffSummary), true);
  assert.equal(isAgentResult(agentResult), true);
  assert.equal(isTaskContract(taskContract), true);
  assert.deepEqual(validateTaskContract(taskContract), []);
});

test("rejects malformed component values", () => {
  assert.equal(isAcceptanceCriterion({ ...criterion, required: "yes" }), false);
  assert.equal(isValidationCommand({ ...validationCommand, timeoutMs: -1 }), false);
  assert.equal(isEscalationPolicy({ maxRetries: -1, triggers: [] }), false);
  assert.equal(isDiffSummary({ ...diffSummary, additions: 1.5 }), false);
  assert.equal(isAgentResult({ ...agentResult, status: "unknown" }), false);
});

test("reports invalid nested values in a task contract", () => {
  const invalidContract = {
    ...taskContract,
    acceptanceCriteria: [{ ...criterion, id: " " }],
    validationCommands: [{ ...validationCommand, command: "" }],
  };

  const issues = validateTaskContract(invalidContract);
  assert.equal(isTaskContract(invalidContract), false);
  assert.ok(issues.some((issue) => issue.includes("acceptanceCriteria[0]")));
  assert.ok(issues.some((issue) => issue.includes("validationCommands[0]")));
});

test("handles non-object input safely", () => {
  assert.equal(isTaskContract(null), false);
  assert.equal(isAgentResult("not a result"), false);
  assert.deepEqual(validateTaskContract(undefined), ["taskContract must be an object"]);
});
