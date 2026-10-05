/** One provider-neutral usage event. Cost must be supplied by the caller; no pricing is guessed. */
export interface UsageRecord {
  readonly requestId: string;
  readonly inputTokens: number;
  readonly outputTokens: number;
  readonly costUsd: number;
  readonly model?: string;
}

export interface UsageTotals {
  readonly requestCount: number;
  readonly inputTokens: number;
  readonly outputTokens: number;
  readonly totalTokens: number;
  readonly costUsd: number;
}

export type FailureKind = string;
export type EscalationReason =
  | "budget_exceeded"
  | "non_retryable_failure"
  | "retries_exhausted"
  | "retryable_failure";

export interface FailureEvent {
  /** Failure count is one-based: 1 is the initial attempt, 2 the first retry, etc. */
  readonly attempt: number;
  readonly kind: FailureKind;
  readonly message?: string;
}

export interface FailureDecision {
  readonly action: "retry" | "escalate";
  readonly attempt: number;
  readonly kind: FailureKind;
  readonly reason: EscalationReason;
  readonly retriesRemaining: number;
  readonly exceededBudgets: readonly ("tokens" | "cost")[];
  readonly message?: string;
}

export interface CostControllerOptions {
  /** Maximum cumulative input + output tokens. */
  readonly maxTotalTokens?: number;
  /** Maximum cumulative USD spend. */
  readonly maxCostUsd?: number;
  /** Number of retries allowed after the initial attempt. */
  readonly maxRetries?: number;
  /** Failure kinds permitted to retry; all other kinds escalate immediately. */
  readonly retryOn?: readonly FailureKind[];
}

const DEFAULT_RETRY_KINDS = ["model_error", "timeout", "rate_limit", "validation_error"] as const;

function isNonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value.trim().length > 0;
}

function validateNonNegativeInteger(value: unknown, name: string): asserts value is number {
  if (typeof value !== "number" || !Number.isInteger(value) || value < 0) {
    throw new TypeError(`${name} must be a non-negative integer`);
  }
}

function validateNonNegativeFinite(value: unknown, name: string): asserts value is number {
  if (typeof value !== "number" || !Number.isFinite(value) || value < 0) {
    throw new TypeError(`${name} must be a non-negative finite number`);
  }
}

/** Tracks caller-reported usage and decides whether failures should be retried or escalated. */
export class CostController {
  private readonly maxTotalTokens: number | undefined;
  private readonly maxCostUsd: number | undefined;
  private readonly maxRetries: number;
  private readonly retryOn: ReadonlySet<string>;
  private readonly records: UsageRecord[] = [];
  private readonly requestIds = new Set<string>();
  private inputTokens = 0;
  private outputTokens = 0;
  private costUsd = 0;

  constructor(options: CostControllerOptions = {}) {
    if (options.maxTotalTokens !== undefined) {
      validateNonNegativeInteger(options.maxTotalTokens, "maxTotalTokens");
    }
    if (options.maxCostUsd !== undefined) validateNonNegativeFinite(options.maxCostUsd, "maxCostUsd");
    this.maxTotalTokens = options.maxTotalTokens;
    this.maxCostUsd = options.maxCostUsd;
    this.maxRetries = options.maxRetries ?? 2;
    validateNonNegativeInteger(this.maxRetries, "maxRetries");
    const retryOn = options.retryOn ?? DEFAULT_RETRY_KINDS;
    if (!Array.isArray(retryOn) || !retryOn.every(isNonEmptyString)) {
      throw new TypeError("retryOn must be an array of non-empty failure kinds");
    }
    this.retryOn = new Set(retryOn);
  }

  /** Add one usage record; duplicate request IDs are rejected to prevent accidental double counting. */
  recordUsage(record: UsageRecord): UsageTotals {
    if (!record || typeof record !== "object") throw new TypeError("usage record must be an object");
    if (!isNonEmptyString(record.requestId)) throw new TypeError("requestId must be a non-empty string");
    if (this.requestIds.has(record.requestId)) throw new TypeError(`duplicate requestId: ${record.requestId}`);
    validateNonNegativeInteger(record.inputTokens, "inputTokens");
    validateNonNegativeInteger(record.outputTokens, "outputTokens");
    validateNonNegativeFinite(record.costUsd, "costUsd");
    if (record.model !== undefined && typeof record.model !== "string") {
      throw new TypeError("model must be a string when provided");
    }

    const stored = { ...record };
    this.records.push(stored);
    this.requestIds.add(record.requestId);
    this.inputTokens += record.inputTokens;
    this.outputTokens += record.outputTokens;
    this.costUsd += record.costUsd;
    return this.getTotals();
  }

  getTotals(): UsageTotals {
    return {
      requestCount: this.records.length,
      inputTokens: this.inputTokens,
      outputTokens: this.outputTokens,
      totalTokens: this.inputTokens + this.outputTokens,
      costUsd: this.costUsd,
    };
  }

  /** Return a defensive copy of usage history. */
  getRecords(): readonly UsageRecord[] {
    return this.records.map((record) => ({ ...record }));
  }

  getExceededBudgets(): readonly ("tokens" | "cost")[] {
    const totals = this.getTotals();
    const exceeded: ("tokens" | "cost")[] = [];
    if (this.maxTotalTokens !== undefined && totals.totalTokens > this.maxTotalTokens) {
      exceeded.push("tokens");
    }
    if (this.maxCostUsd !== undefined && totals.costUsd > this.maxCostUsd) exceeded.push("cost");
    return exceeded;
  }

  isBudgetExceeded(): boolean {
    return this.getExceededBudgets().length > 0;
  }

  /**
   * Decide how to handle a failure. Budget overruns and non-retryable errors escalate immediately;
   * otherwise retries are allowed up to maxRetries, after which the failure escalates.
   */
  decideOnFailure(event: FailureEvent): FailureDecision {
    if (!event || typeof event !== "object") throw new TypeError("failure event must be an object");
    validateNonNegativeInteger(event.attempt, "attempt");
    if (event.attempt < 1) throw new TypeError("attempt must be at least 1");
    if (!isNonEmptyString(event.kind)) throw new TypeError("kind must be a non-empty string");
    if (event.message !== undefined && typeof event.message !== "string") {
      throw new TypeError("message must be a string when provided");
    }

    const exceededBudgets = this.getExceededBudgets();
    let action: FailureDecision["action"];
    let reason: EscalationReason;
    if (exceededBudgets.length > 0) {
      action = "escalate";
      reason = "budget_exceeded";
    } else if (!this.retryOn.has(event.kind)) {
      action = "escalate";
      reason = "non_retryable_failure";
    } else if (event.attempt > this.maxRetries) {
      action = "escalate";
      reason = "retries_exhausted";
    } else {
      action = "retry";
      reason = "retryable_failure";
    }

    return {
      action,
      attempt: event.attempt,
      kind: event.kind,
      reason,
      retriesRemaining: Math.max(0, this.maxRetries - event.attempt),
      exceededBudgets,
      ...(event.message === undefined ? {} : { message: event.message }),
    };
  }
}
