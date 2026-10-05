import assert from "node:assert/strict";
import test from "node:test";
import {
  ModelRouter,
  NoAvailableModelError,
  type CostTier,
  type ModelDefinition,
  type ModelRequest,
  type ModelProvider,
  type ProviderAdapter,
} from "./model-gateway.ts";

function fakeAdapter(
  provider: ModelProvider,
  models: readonly ModelDefinition[],
  onComplete: () => void = () => undefined,
): ProviderAdapter {
  return {
    provider,
    models,
    async complete(_request, model) {
      onComplete();
      return { content: `answer from ${model.id}`, usage: { inputTokens: 4, outputTokens: 2 } };
    },
  };
}

function model(
  id: string,
  costTier: CostTier,
  taskTypes: readonly string[] = ["summarize"],
  capabilities: readonly string[] = [],
): ModelDefinition {
  return { id, costTier, taskTypes, capabilities };
}

const request: ModelRequest = {
  taskType: "summarize",
  input: "Summarize this text.",
  costTier: "balanced",
};

test("supports injectable adapters for each named provider without network dependencies", async () => {
  const providers: readonly ModelProvider[] = ["claude", "deepseek", "kimi", "gemini", "openai"];
  for (const provider of providers) {
    const router = new ModelRouter([fakeAdapter(provider, [model(`${provider}-model`, "balanced")])]);
    const response = await router.route({ ...request, capabilities: [] });
    assert.equal(response.provider, provider);
    assert.equal(response.model, `${provider}-model`);
  }
});

test("selects by task type, all required capabilities, and exact cost tier", async () => {
  let chosenCalls = 0;
  let skippedCalls = 0;
  const router = new ModelRouter([
    fakeAdapter("claude", [model("wrong-task", "balanced", ["translate"], ["tools"])], () => skippedCalls++),
    fakeAdapter("deepseek", [model("missing-vision", "balanced", ["summarize"], ["tools"])], () => skippedCalls++),
    fakeAdapter("kimi", [model("cheap-tools", "low", ["summarize"], ["tools", "vision"])], () => skippedCalls++),
    fakeAdapter("gemini", [model("balanced-tools-vision", "balanced", ["summarize"], ["tools", "vision"])], () => chosenCalls++),
  ]);

  const response = await router.route({ ...request, capabilities: ["tools", "vision"] });
  assert.equal(response.provider, "gemini");
  assert.equal(response.model, "balanced-tools-vision");
  assert.equal(response.content, "answer from balanced-tools-vision");
  assert.deepEqual(response.usage, { inputTokens: 4, outputTokens: 2 });
  assert.equal(chosenCalls, 1);
  assert.equal(skippedCalls, 0);
});

test("falls back to the nearest available cost tier and uses registration order for ties", () => {
  const router = new ModelRouter([
    fakeAdapter("deepseek", [model("low-first", "low")]),
    fakeAdapter("kimi", [model("high-second", "high")]),
  ]);
  const selected = router.select(request);
  assert.equal(selected.adapter.provider, "deepseek");
  assert.equal(selected.model.id, "low-first");
});

test("supports wildcard task types when choosing among compatible models", () => {
  const router = new ModelRouter([
    fakeAdapter("openai", [model("general", "balanced", ["*"], ["json"])]),
  ]);
  assert.equal(
    router.select({ ...request, taskType: "extract", capabilities: ["json"] }).model.id,
    "general",
  );
});

test("throws a descriptive error when no candidate meets task and capability requirements", () => {
  const router = new ModelRouter([
    fakeAdapter("claude", [model("text-only", "balanced", ["summarize"], [])]),
  ]);
  assert.throws(
    () => router.select({ ...request, taskType: "translate", capabilities: ["vision"] }),
    (error: unknown) => {
      if (!(error instanceof NoAvailableModelError)) return false;
      assert.match(error.message, /translate/);
      assert.match(error.message, /vision/);
      return true;
    },
  );
});

test("rejects duplicate provider adapters and malformed requests", () => {
  assert.throws(
    () => new ModelRouter([
      fakeAdapter("claude", [model("one", "balanced")]),
      fakeAdapter("claude", [model("two", "balanced")]),
    ]),
    /only one adapter may be registered/,
  );

  const router = new ModelRouter([fakeAdapter("claude", [model("one", "balanced")])]);
  assert.throws(() => router.select({ ...request, taskType: " " }), /taskType/);
  assert.throws(() => router.select({ ...request, capabilities: [""] }), /capabilities/);
});
