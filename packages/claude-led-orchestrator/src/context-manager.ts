/**
 * Builds a compact, deterministic prompt context from explicitly allowlisted files.
 * Token counts are estimates (four UTF-16 code units per token by default), not provider billing data.
 */
export interface ContextFile {
  readonly path: string;
  readonly summary?: string;
  /** A focused, already-computed diff for this file. */
  readonly diff?: string;
}

export interface ContextBuildRequest {
  readonly task: string;
  readonly allowedFiles: readonly string[];
  readonly files: readonly ContextFile[];
  /** Overrides the manager's default budget. */
  readonly tokenBudget?: number;
}

export type ContextSectionKind = "summary" | "diff";

export interface IncludedContextSection {
  readonly path: string;
  readonly kind: ContextSectionKind;
}

export interface ContextBuildResult {
  readonly context: string;
  readonly estimatedTokens: number;
  readonly tokenBudget: number;
  readonly includedFiles: readonly string[];
  readonly omittedFiles: readonly string[];
  readonly includedSections: readonly IncludedContextSection[];
  readonly taskTruncated: boolean;
}

export interface ContextManagerOptions {
  readonly tokenBudget?: number;
  /** Override for deterministic tests or a caller's local token estimator. */
  readonly estimateTokens?: (text: string) => number;
}

/** A small, dependency-free heuristic suitable for enforcing relative context budgets. */
export function estimateContextTokens(text: string): number {
  return Math.ceil(text.length / 4);
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value.trim().length > 0;
}

function validateBudget(value: unknown, name: string): asserts value is number {
  if (typeof value !== "number" || !Number.isInteger(value) || value < 1) {
    throw new TypeError(`${name} must be a positive integer`);
  }
}

/**
 * Packs a task prompt plus only allowlisted summaries and diffs. It never includes full file
 * contents; sections are added in allowlist order until the token budget is full.
 */
export class ContextManager {
  private readonly defaultTokenBudget: number;
  private readonly tokenEstimator: (text: string) => number;

  constructor(options: ContextManagerOptions = {}) {
    this.defaultTokenBudget = options.tokenBudget ?? 8_000;
    validateBudget(this.defaultTokenBudget, "tokenBudget");
    this.tokenEstimator = options.estimateTokens ?? estimateContextTokens;
    if (typeof this.tokenEstimator !== "function") {
      throw new TypeError("estimateTokens must be a function");
    }
  }

  build(request: ContextBuildRequest): ContextBuildResult {
    if (!request || typeof request !== "object") throw new TypeError("request must be an object");
    if (!isNonEmptyString(request.task)) throw new TypeError("task must be a non-empty string");
    if (!Array.isArray(request.allowedFiles) || !request.allowedFiles.every(isNonEmptyString)) {
      throw new TypeError("allowedFiles must be an array of non-empty paths");
    }
    if (!Array.isArray(request.files)) throw new TypeError("files must be an array");

    const budget = request.tokenBudget ?? this.defaultTokenBudget;
    validateBudget(budget, "tokenBudget");

    const byPath = new Map<string, ContextFile>();
    for (const file of request.files) {
      if (!file || typeof file !== "object" || !isNonEmptyString(file.path)) {
        throw new TypeError("each file must have a non-empty path");
      }
      if (byPath.has(file.path)) throw new TypeError(`duplicate file path: ${file.path}`);
      for (const field of ["summary", "diff"] as const) {
        if (file[field] !== undefined && typeof file[field] !== "string") {
          throw new TypeError(`file ${file.path} ${field} must be a string when provided`);
        }
      }
      byPath.set(file.path, file);
    }

    const allowedPaths = [...new Set(request.allowedFiles)];
    const blocks: { path: string; kind: ContextSectionKind; text: string }[] = [];
    for (const path of allowedPaths) {
      const file = byPath.get(path);
      if (!file) continue;

      const hasSummary = isNonEmptyString(file.summary);
      const hasDiff = isNonEmptyString(file.diff);
      if (hasSummary) blocks.push({ path, kind: "summary", text: `Summary ${path}:\n${file.summary!.trim()}` });
      if (hasDiff) blocks.push({ path, kind: "diff", text: `Diff ${path}:\n${file.diff!.trim()}` });
    }

    const taskText = `Task:\n${request.task.trim()}`;
    let selected = [taskText];
    let taskTruncated = false;
    if (this.count(this.render(selected)) > budget) {
      const shortened = this.truncateTask(request.task.trim(), budget);
      selected = [`Task:\n${shortened}`];
      taskTruncated = true;
    }

    const includedSections: IncludedContextSection[] = [];
    for (const block of blocks) {
      const candidate = [...selected, block.text];
      if (this.count(this.render(candidate)) <= budget) {
        selected = candidate;
        includedSections.push({ path: block.path, kind: block.kind });
      }
    }

    const includedFiles = [...new Set(includedSections.map((section) => section.path))];
    const includedSet = new Set(includedFiles);
    const resultContext = this.render(selected);
    return {
      context: resultContext,
      estimatedTokens: this.count(resultContext),
      tokenBudget: budget,
      includedFiles,
      omittedFiles: allowedPaths.filter((path) => !includedSet.has(path)),
      includedSections,
      taskTruncated,
    };
  }

  private count(text: string): number {
    const count = this.tokenEstimator(text);
    if (typeof count !== "number" || !Number.isFinite(count) || !Number.isInteger(count) || count < 0) {
      throw new TypeError("estimateTokens must return a non-negative integer");
    }
    return count;
  }

  private render(blocks: readonly string[]): string {
    return blocks.join("\n\n");
  }

  private truncateTask(task: string, budget: number): string {
    const fits = (value: string) => this.count(this.render([`Task:\n${value}`])) <= budget;
    let low = 0;
    let high = task.length;
    let best = "";
    while (low <= high) {
      const middle = Math.floor((low + high) / 2);
      const candidate = middle < task.length ? `${task.slice(0, middle)}…` : task;
      if (fits(candidate)) {
        best = candidate;
        low = middle + 1;
      } else {
        high = middle - 1;
      }
    }
    if (!best) throw new RangeError("tokenBudget is too small to include the task heading");
    return best;
  }
}
