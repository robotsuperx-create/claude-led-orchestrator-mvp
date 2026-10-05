import type { ModelProvider } from "./model-gateway.ts";

export interface PlannedSubtask {
  id: string;
  title: string;
  instructions: string;
  workerId: string;
  provider: ModelProvider;
}

export interface ExecutionPlan {
  summary: string;
  subtasks: readonly PlannedSubtask[];
}

export interface PlanRequest {
  /** The executive planner is always Claude; the gateway remains injected. */
  provider: "claude";
  task: string;
  memoryContext: string;
}

export interface ReviewRequest {
  /** The executive reviewer is always Claude; the gateway remains injected. */
  provider: "claude";
  task: string;
  plan: ExecutionPlan;
  results: readonly CollectedTaskResult[];
  validation: ValidationReport;
}

export interface ReviewDecision {
  decision: "approve" | "request_changes";
  summary: string;
  issues?: readonly string[];
}

/** Pure application boundary: callers provide any real, mock, or local model implementation. */
export interface ModelGateway {
  plan(request: PlanRequest): Promise<ExecutionPlan>;
  review(request: ReviewRequest): Promise<ReviewDecision>;
}

export interface WorkerRequest {
  task: PlannedSubtask;
  attempt: number;
  /** Prior failure summary, provided to the worker on a retry. */
  previousFailure?: string;
}

export interface WorkerExecution {
  status: "completed" | "failed" | "blocked";
  summary: string;
  output?: unknown;
  error?: string;
}

/** Executes a delegated task using the target specified by the plan. */
export interface WorkerRuntime {
  execute(request: WorkerRequest): Promise<WorkerExecution>;
}

export interface CollectedAttempt {
  attempt: number;
  execution: WorkerExecution;
}

export interface CollectedTaskResult {
  task: PlannedSubtask;
  attempts: readonly CollectedAttempt[];
  finalExecution: WorkerExecution;
}

export interface ValidationReport {
  passed: boolean;
  issues: readonly string[];
}

/** Validates collected work without prescribing a command runner or external service. */
export interface Validator {
  validate(input: {
    task: string;
    plan: ExecutionPlan;
    results: readonly CollectedTaskResult[];
  }): Promise<ValidationReport>;
}

export interface MergeDecision {
  decision: "merge" | "hold";
  reasons: readonly string[];
}

export interface OrchestrationResult {
  task: string;
  plan: ExecutionPlan;
  results: readonly CollectedTaskResult[];
  validation: ValidationReport;
  review: ReviewDecision;
  mergeDecision: MergeDecision;
}

/** Project memory is injected; this class never reads or writes a store directly. */
export interface ProjectMemory {
  readContext(task: string): Promise<string>;
  recordOutcome(input: {
    task: string;
    summary: string;
    mergeDecision: MergeDecision;
  }): Promise<void>;
}

export interface ExecutiveOrchestratorDependencies {
  modelGateway: ModelGateway;
  workerRuntime: WorkerRuntime;
  projectMemory: ProjectMemory;
  validator: Validator;
}

export interface ExecutiveOrchestratorOptions {
  /** Number of retries after the initial worker attempt. Defaults to one retry. */
  maxRetries?: number;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function validatePlan(plan: ExecutionPlan): void {
  if (!plan || typeof plan !== "object" || typeof plan.summary !== "string") {
    throw new TypeError("ModelGateway.plan() must return a plan with a summary");
  }
  if (!Array.isArray(plan.subtasks)) {
    throw new TypeError("ModelGateway.plan() must return a subtasks array");
  }

  const ids = new Set<string>();
  for (const [index, subtask] of plan.subtasks.entries()) {
    if (
      !subtask ||
      typeof subtask.id !== "string" ||
      subtask.id.trim() === "" ||
      typeof subtask.title !== "string" ||
      typeof subtask.instructions !== "string" ||
      typeof subtask.workerId !== "string" ||
      subtask.workerId.trim() === "" ||
      typeof subtask.provider !== "string"
    ) {
      throw new TypeError(`ModelGateway.plan() returned an invalid subtask at index ${index}`);
    }
    if (ids.has(subtask.id)) throw new TypeError(`ModelGateway.plan() returned duplicate subtask id: ${subtask.id}`);
    ids.add(subtask.id);
  }
}

/**
 * Coordinates plan -> delegate/collect -> validate -> review -> merge decision.
 * All I/O is delegated to injected interfaces; the class has no provider SDK or service dependency.
 */
export class ExecutiveOrchestrator {
  private readonly maxRetries: number;
  private readonly dependencies: ExecutiveOrchestratorDependencies;

  constructor(
    dependencies: ExecutiveOrchestratorDependencies,
    options: ExecutiveOrchestratorOptions = {},
  ) {
    this.dependencies = dependencies;
    const maxRetries = options.maxRetries ?? 1;
    if (!Number.isInteger(maxRetries) || maxRetries < 0) {
      throw new TypeError("maxRetries must be a non-negative integer");
    }
    this.maxRetries = maxRetries;
  }

  async run(task: string): Promise<OrchestrationResult> {
    if (typeof task !== "string" || task.trim() === "") {
      throw new TypeError("task must be a non-empty string");
    }

    const memoryContext = await this.dependencies.projectMemory.readContext(task);
    const plan = await this.dependencies.modelGateway.plan({
      provider: "claude",
      task,
      memoryContext,
    });
    validatePlan(plan);

    const results: CollectedTaskResult[] = [];
    for (const subtask of plan.subtasks) {
      results.push(await this.executeWithRetries(subtask));
    }

    const validation = await this.dependencies.validator.validate({ task, plan, results });
    const review = await this.dependencies.modelGateway.review({
      provider: "claude",
      task,
      plan,
      results,
      validation,
    });

    const reasons: string[] = [];
    if (results.some((result) => result.finalExecution.status !== "completed")) {
      reasons.push("One or more delegated tasks did not complete successfully.");
    }
    if (!validation.passed) {
      reasons.push(...(validation.issues.length > 0 ? validation.issues : ["Validation did not pass."]));
    }
    if (review.decision !== "approve") {
      reasons.push(...(review.issues ?? []));
      if (reasons.length === 0) reasons.push("Claude requested changes during review.");
    }

    const mergeDecision: MergeDecision = {
      decision: reasons.length === 0 ? "merge" : "hold",
      reasons,
    };
    const result: OrchestrationResult = { task, plan, results, validation, review, mergeDecision };

    await this.dependencies.projectMemory.recordOutcome({
      task,
      summary: review.summary,
      mergeDecision,
    });
    return result;
  }

  private async executeWithRetries(task: PlannedSubtask): Promise<CollectedTaskResult> {
    const attempts: CollectedAttempt[] = [];
    let previousFailure: string | undefined;

    for (let attempt = 1; attempt <= this.maxRetries + 1; attempt += 1) {
      let execution: WorkerExecution;
      try {
        execution = await this.dependencies.workerRuntime.execute({
          task,
          attempt,
          ...(previousFailure === undefined ? {} : { previousFailure }),
        });
      } catch (error) {
        const message = errorMessage(error);
        execution = { status: "failed", summary: `Worker threw: ${message}`, error: message };
      }
      attempts.push({ attempt, execution });

      if (execution.status !== "failed") break;
      previousFailure = execution.error ?? execution.summary;
    }

    const finalExecution = attempts[attempts.length - 1]?.execution;
    if (!finalExecution) throw new Error(`No worker attempt was made for subtask ${task.id}`);
    return { task, attempts, finalExecution };
  }
}
