/** Supported providers. Provider adapters are supplied by callers; this module performs no I/O. */
export const MODEL_PROVIDERS = ["claude", "deepseek", "kimi", "gemini", "openai"] as const;

export type ModelProvider = (typeof MODEL_PROVIDERS)[number];
export type CostTier = "low" | "balanced" | "high";
export type ModelCapability = string;

/** A model's routing metadata; it contains no credentials or provider SDK objects. */
export interface ModelDefinition {
  id: string;
  costTier: CostTier;
  /** Task types this model can handle. Use "*" to opt into every task type. */
  taskTypes: readonly string[];
  /** Capabilities offered by this model (for example, "vision" or "tools"). */
  capabilities: readonly ModelCapability[];
}

/** Provider-neutral input passed unchanged to the chosen adapter. */
export interface ModelRequest {
  taskType: string;
  input: string;
  costTier: CostTier;
  capabilities?: readonly ModelCapability[];
}

export interface ModelUsage {
  inputTokens?: number;
  outputTokens?: number;
  totalTokens?: number;
}

/** Provider-neutral result returned by the gateway. */
export interface ModelResponse {
  provider: ModelProvider;
  model: string;
  content: string;
  usage?: ModelUsage;
  finishReason?: string;
  metadata?: Readonly<Record<string, unknown>>;
}

/** The response payload an adapter produces before the gateway adds its identity. */
export type ProviderCompletion = Omit<ModelResponse, "provider" | "model">;

/** Injectable adapter boundary. Implementations may wrap SDKs, test doubles, or local models. */
export interface ProviderAdapter {
  readonly provider: ModelProvider;
  readonly models: readonly ModelDefinition[];
  complete(request: ModelRequest, model: ModelDefinition): Promise<ProviderCompletion>;
}

export interface ModelRoute {
  readonly adapter: ProviderAdapter;
  readonly model: ModelDefinition;
}

const COST_TIER_RANK: Readonly<Record<CostTier, number>> = {
  low: 0,
  balanced: 1,
  high: 2,
};

const PROVIDER_SET: ReadonlySet<string> = new Set(MODEL_PROVIDERS);
const COST_TIER_SET: ReadonlySet<string> = new Set(Object.keys(COST_TIER_RANK));

function isNonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value.trim().length > 0;
}

function validateRequest(request: ModelRequest): void {
  if (!request || typeof request !== "object") throw new TypeError("request must be an object");
  if (!isNonEmptyString(request.taskType)) throw new TypeError("request.taskType must be a non-empty string");
  if (typeof request.input !== "string") throw new TypeError("request.input must be a string");
  if (!COST_TIER_SET.has(request.costTier)) {
    throw new TypeError("request.costTier must be low, balanced, or high");
  }
  if (
    request.capabilities !== undefined &&
    (!Array.isArray(request.capabilities) || !request.capabilities.every(isNonEmptyString))
  ) {
    throw new TypeError("request.capabilities must be an array of non-empty strings");
  }
}

function validateAdapter(adapter: ProviderAdapter): void {
  if (!adapter || typeof adapter !== "object") throw new TypeError("each adapter must be an object");
  if (!PROVIDER_SET.has(adapter.provider)) {
    throw new TypeError(`unsupported provider: ${String(adapter.provider)}`);
  }
  if (!Array.isArray(adapter.models) || adapter.models.length === 0) {
    throw new TypeError(`adapter ${adapter.provider} must expose at least one model`);
  }
  if (typeof adapter.complete !== "function") {
    throw new TypeError(`adapter ${adapter.provider} must implement complete()`);
  }

  const modelIds = new Set<string>();
  for (const model of adapter.models) {
    if (!model || !isNonEmptyString(model.id)) {
      throw new TypeError(`adapter ${adapter.provider} models must have non-empty ids`);
    }
    if (modelIds.has(model.id)) {
      throw new TypeError(`adapter ${adapter.provider} has duplicate model id: ${model.id}`);
    }
    modelIds.add(model.id);
    if (!COST_TIER_SET.has(model.costTier)) {
      throw new TypeError(`model ${model.id} has an unsupported cost tier`);
    }
    if (!Array.isArray(model.taskTypes) || !model.taskTypes.every(isNonEmptyString)) {
      throw new TypeError(`model ${model.id} taskTypes must be an array of non-empty strings`);
    }
    if (!Array.isArray(model.capabilities) || !model.capabilities.every(isNonEmptyString)) {
      throw new TypeError(`model ${model.id} capabilities must be an array of non-empty strings`);
    }
  }
}

/**
 * Routes requests to injected provider adapters. Candidates must support the task and every
 * requested capability. Among them, the closest cost tier wins; ties follow adapter/model order.
 */
export class ModelRouter {
  private readonly adapters: readonly ProviderAdapter[];

  constructor(adapters: readonly ProviderAdapter[]) {
    if (!Array.isArray(adapters)) throw new TypeError("adapters must be an array");
    const providers = new Set<ModelProvider>();
    for (const adapter of adapters) {
      validateAdapter(adapter);
      if (providers.has(adapter.provider)) {
        throw new TypeError(`only one adapter may be registered for provider ${adapter.provider}`);
      }
      providers.add(adapter.provider);
    }
    this.adapters = [...adapters];
  }

  /** Select a compatible adapter/model without invoking it. */
  select(request: ModelRequest): ModelRoute {
    validateRequest(request);
    const requestedCapabilities = request.capabilities ?? [];
    let best: ModelRoute | undefined;
    let bestCostDistance = Number.POSITIVE_INFINITY;

    for (const adapter of this.adapters) {
      for (const model of adapter.models) {
        if (!model.taskTypes.includes("*") && !model.taskTypes.includes(request.taskType)) continue;
        if (!requestedCapabilities.every((capability) => model.capabilities.includes(capability))) continue;

        const costDistance = Math.abs(COST_TIER_RANK[model.costTier] - COST_TIER_RANK[request.costTier]);
        if (costDistance < bestCostDistance) {
          best = { adapter, model };
          bestCostDistance = costDistance;
        }
      }
    }

    if (!best) {
      throw new NoAvailableModelError(request.taskType, request.costTier, requestedCapabilities);
    }
    return best;
  }

  /** Select a model and invoke only its injected adapter. */
  async route(request: ModelRequest): Promise<ModelResponse> {
    const { adapter, model } = this.select(request);
    const completion = await adapter.complete(request, model);
    if (!completion || typeof completion.content !== "string") {
      throw new TypeError(`adapter ${adapter.provider} returned an invalid completion`);
    }
    return { ...completion, provider: adapter.provider, model: model.id };
  }
}

export class NoAvailableModelError extends Error {
  readonly taskType: string;
  readonly costTier: CostTier;
  readonly capabilities: readonly ModelCapability[];

  constructor(taskType: string, costTier: CostTier, capabilities: readonly ModelCapability[]) {
    const required = capabilities.length > 0 ? ` with capabilities [${capabilities.join(", ")}]` : "";
    super(`No model available for task "${taskType}" at cost tier "${costTier}"${required}`);
    this.name = "NoAvailableModelError";
    this.taskType = taskType;
    this.costTier = costTier;
    this.capabilities = [...capabilities];
  }
}
