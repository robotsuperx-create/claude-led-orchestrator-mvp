import assert from "node:assert/strict";
import test from "node:test";
import {
  ExecutiveOrchestrator,
  type ExecutionPlan,
  type MergeDecision,
  type ReviewDecision,
  type WorkerRequest,
} from "./executive-orchestrator.ts";

const plan: ExecutionPlan = {
  summary: "Implement the requested change.",
  subtasks: [
    {
      id: "implement",
      title: "Implement feature",
      instructions: "Write the feature and its tests.",
      workerId: "deepseek-worker",
      provider: "deepseek",
    },
  ],
};

test("Claude plans and reviews while DeepSeek delegation is retried after failure", async () => {
  const planProviders: string[] = [];
  const reviewProviders: string[] = [];
  const workerCalls: WorkerRequest[] = [];
  const memoryWrites: { task: string; summary: string; mergeDecision: MergeDecision }[] = [];
  let validationCalls = 0;
  const review: ReviewDecision = { decision: "approve", summary: "Implementation looks good." };

  const orchestrator = new ExecutiveOrchestrator(
    {
      modelGateway: {
        async plan(request) {
          planProviders.push(request.provider);
          assert.equal(request.task, "Add retry handling");
          assert.equal(request.memoryContext, "Existing project conventions");
          return plan;
        },
        async review(request) {
          reviewProviders.push(request.provider);
          assert.equal(request.results[0]?.attempts.length, 2);
          assert.equal(request.validation.passed, true);
          return review;
        },
      },
      workerRuntime: {
        async execute(request) {
          workerCalls.push(request);
          if (request.attempt === 1) {
            return { status: "failed", summary: "Transient worker failure", error: "temporary error" };
          }
          return { status: "completed", summary: "Feature implemented", output: { changed: true } };
        },
      },
      projectMemory: {
        async readContext() {
          return "Existing project conventions";
        },
        async recordOutcome(input) {
          memoryWrites.push(input);
        },
      },
      validator: {
        async validate({ results }) {
          validationCalls += 1;
          assert.equal(results[0]?.finalExecution.status, "completed");
          return { passed: true, issues: [] };
        },
      },
    },
    { maxRetries: 1 },
  );

  const result = await orchestrator.run("Add retry handling");

  assert.deepEqual(planProviders, ["claude"]);
  assert.deepEqual(reviewProviders, ["claude"]);
  assert.deepEqual(workerCalls.map(({ task }) => task.provider), ["deepseek", "deepseek"]);
  assert.deepEqual(workerCalls.map(({ attempt }) => attempt), [1, 2]);
  assert.equal(workerCalls[1]?.previousFailure, "temporary error");
  assert.deepEqual(result.results[0]?.attempts.map(({ execution }) => execution.status), ["failed", "completed"]);
  assert.equal(validationCalls, 1);
  assert.equal(result.mergeDecision.decision, "merge");
  assert.equal(memoryWrites[0]?.mergeDecision.decision, "merge");
  assert.equal(memoryWrites.length, 1);
});

test("holds the merge decision if validation fails despite an approving review", async () => {
  const orchestrator = new ExecutiveOrchestrator({
    modelGateway: {
      async plan() {
        return { ...plan, subtasks: [] };
      },
      async review() {
        return { decision: "approve", summary: "Looks fine." };
      },
    },
    workerRuntime: {
      async execute() {
        throw new Error("No work expected");
      },
    },
    projectMemory: {
      async readContext() {
        return "";
      },
      async recordOutcome() {},
    },
    validator: {
      async validate() {
        return { passed: false, issues: ["Required check failed."] };
      },
    },
  });

  const result = await orchestrator.run("Run checks");
  assert.deepEqual(result.mergeDecision, {
    decision: "hold",
    reasons: ["Required check failed."],
  });
});
