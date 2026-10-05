/** A single condition that must be satisfied for a task to be accepted. */
export interface AcceptanceCriterion {
  id: string;
  description: string;
  required: boolean;
}

/** A command that can be run to validate a task's implementation. */
export interface ValidationCommand {
  id: string;
  command: string;
  description?: string;
  required: boolean;
  timeoutMs?: number;
}

/** The conditions, checks, and escalation behavior associated with a task. */
export interface TaskContract {
  id: string;
  title: string;
  description: string;
  acceptanceCriteria: AcceptanceCriterion[];
  validationCommands: ValidationCommand[];
  escalationPolicy: EscalationPolicy;
}

/** A normalized summary of the file changes produced by an agent. */
export interface DiffSummary {
  filesChanged: string[];
  additions: number;
  deletions: number;
  summary: string;
}

export type AgentResultStatus = "completed" | "failed" | "blocked";

/** The outcome returned by an agent after attempting a task. */
export interface AgentResult {
  agentId: string;
  status: AgentResultStatus;
  summary: string;
  diffSummary: DiffSummary;
  completedCriterionIds: string[];
  failedCriterionIds: string[];
}

export type EscalationTrigger =
  | "agent_failure"
  | "validation_failure"
  | "acceptance_criteria_unmet"
  | "timeout";

/** Defines when an unsuccessful attempt should be retried or escalated. */
export interface EscalationPolicy {
  maxRetries: number;
  triggers: EscalationTrigger[];
  escalateTo?: string;
}

/** A concise issue list suitable for displaying validation errors to callers. */
export type ContractValidationIssue = string;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value.trim().length > 0;
}

function isNonNegativeInteger(value: unknown): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= 0;
}

function isStringArray(value: unknown): value is string[] {
  return Array.isArray(value) && value.every((item) => typeof item === "string");
}

const ESCALATION_TRIGGERS: readonly EscalationTrigger[] = [
  "agent_failure",
  "validation_failure",
  "acceptance_criteria_unmet",
  "timeout",
];

/** Return validation issues for an acceptance criterion. */
export function validateAcceptanceCriterion(value: unknown): ContractValidationIssue[] {
  if (!isRecord(value)) return ["criterion must be an object"];

  const issues: ContractValidationIssue[] = [];
  if (!isNonEmptyString(value.id)) issues.push("criterion.id must be a non-empty string");
  if (!isNonEmptyString(value.description)) {
    issues.push("criterion.description must be a non-empty string");
  }
  if (typeof value.required !== "boolean") issues.push("criterion.required must be a boolean");
  return issues;
}

/** Return validation issues for a validation command. */
export function validateValidationCommand(value: unknown): ContractValidationIssue[] {
  if (!isRecord(value)) return ["validation command must be an object"];

  const issues: ContractValidationIssue[] = [];
  if (!isNonEmptyString(value.id)) issues.push("validation command.id must be a non-empty string");
  if (!isNonEmptyString(value.command)) {
    issues.push("validation command.command must be a non-empty string");
  }
  if (value.description !== undefined && typeof value.description !== "string") {
    issues.push("validation command.description must be a string when provided");
  }
  if (typeof value.required !== "boolean") {
    issues.push("validation command.required must be a boolean");
  }
  if (value.timeoutMs !== undefined && !isNonNegativeInteger(value.timeoutMs)) {
    issues.push("validation command.timeoutMs must be a non-negative integer when provided");
  }
  return issues;
}

/** Return validation issues for an escalation policy. */
export function validateEscalationPolicy(value: unknown): ContractValidationIssue[] {
  if (!isRecord(value)) return ["escalationPolicy must be an object"];

  const issues: ContractValidationIssue[] = [];
  if (!isNonNegativeInteger(value.maxRetries)) {
    issues.push("escalationPolicy.maxRetries must be a non-negative integer");
  }
  if (!Array.isArray(value.triggers)) {
    issues.push("escalationPolicy.triggers must be an array");
  } else if (
    !value.triggers.every(
      (trigger): trigger is EscalationTrigger =>
        typeof trigger === "string" && ESCALATION_TRIGGERS.includes(trigger as EscalationTrigger),
    )
  ) {
    issues.push("escalationPolicy.triggers contains an unsupported trigger");
  }
  if (value.escalateTo !== undefined && !isNonEmptyString(value.escalateTo)) {
    issues.push("escalationPolicy.escalateTo must be a non-empty string when provided");
  }
  return issues;
}

/** Return validation issues for a diff summary. */
export function validateDiffSummary(value: unknown): ContractValidationIssue[] {
  if (!isRecord(value)) return ["diffSummary must be an object"];

  const issues: ContractValidationIssue[] = [];
  if (!isStringArray(value.filesChanged)) {
    issues.push("diffSummary.filesChanged must be an array of strings");
  }
  if (!isNonNegativeInteger(value.additions)) {
    issues.push("diffSummary.additions must be a non-negative integer");
  }
  if (!isNonNegativeInteger(value.deletions)) {
    issues.push("diffSummary.deletions must be a non-negative integer");
  }
  if (typeof value.summary !== "string") issues.push("diffSummary.summary must be a string");
  return issues;
}

/** Return validation issues for an agent result. */
export function validateAgentResult(value: unknown): ContractValidationIssue[] {
  if (!isRecord(value)) return ["agentResult must be an object"];

  const issues: ContractValidationIssue[] = [];
  if (!isNonEmptyString(value.agentId)) issues.push("agentResult.agentId must be a non-empty string");
  if (value.status !== "completed" && value.status !== "failed" && value.status !== "blocked") {
    issues.push("agentResult.status must be completed, failed, or blocked");
  }
  if (typeof value.summary !== "string") issues.push("agentResult.summary must be a string");
  if (!isStringArray(value.completedCriterionIds)) {
    issues.push("agentResult.completedCriterionIds must be an array of strings");
  }
  if (!isStringArray(value.failedCriterionIds)) {
    issues.push("agentResult.failedCriterionIds must be an array of strings");
  }
  issues.push(...validateDiffSummary(value.diffSummary));
  return issues;
}

/** Return validation issues for a task contract, including its nested values. */
export function validateTaskContract(value: unknown): ContractValidationIssue[] {
  if (!isRecord(value)) return ["taskContract must be an object"];

  const issues: ContractValidationIssue[] = [];
  if (!isNonEmptyString(value.id)) issues.push("taskContract.id must be a non-empty string");
  if (!isNonEmptyString(value.title)) issues.push("taskContract.title must be a non-empty string");
  if (!isNonEmptyString(value.description)) {
    issues.push("taskContract.description must be a non-empty string");
  }
  if (!Array.isArray(value.acceptanceCriteria)) {
    issues.push("taskContract.acceptanceCriteria must be an array");
  } else {
    value.acceptanceCriteria.forEach((criterion: unknown, index: number) => {
      issues.push(
        ...validateAcceptanceCriterion(criterion).map(
          (issue) => `taskContract.acceptanceCriteria[${index}]: ${issue}`,
        ),
      );
    });
  }
  if (!Array.isArray(value.validationCommands)) {
    issues.push("taskContract.validationCommands must be an array");
  } else {
    value.validationCommands.forEach((command: unknown, index: number) => {
      issues.push(
        ...validateValidationCommand(command).map(
          (issue) => `taskContract.validationCommands[${index}]: ${issue}`,
        ),
      );
    });
  }
  issues.push(...validateEscalationPolicy(value.escalationPolicy));
  return issues;
}

/** Type guard for callers that need a safe runtime check. */
export function isAcceptanceCriterion(value: unknown): value is AcceptanceCriterion {
  return validateAcceptanceCriterion(value).length === 0;
}

/** Type guard for callers that need a safe runtime check. */
export function isValidationCommand(value: unknown): value is ValidationCommand {
  return validateValidationCommand(value).length === 0;
}

/** Type guard for callers that need a safe runtime check. */
export function isEscalationPolicy(value: unknown): value is EscalationPolicy {
  return validateEscalationPolicy(value).length === 0;
}

/** Type guard for callers that need a safe runtime check. */
export function isDiffSummary(value: unknown): value is DiffSummary {
  return validateDiffSummary(value).length === 0;
}

/** Type guard for callers that need a safe runtime check. */
export function isAgentResult(value: unknown): value is AgentResult {
  return validateAgentResult(value).length === 0;
}

/** Type guard for callers that need a safe runtime check. */
export function isTaskContract(value: unknown): value is TaskContract {
  return validateTaskContract(value).length === 0;
}
