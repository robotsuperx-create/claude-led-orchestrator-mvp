import assert from "node:assert/strict";
import test from "node:test";
import { SkillRegistry, type SkillManifest } from "./skill-registry.ts";

const codingSkill: SkillManifest = {
  id: "code-review",
  name: "Code review",
  description: "Reviews source changes.",
  category: "coding",
  source: { repository: "example/code-review", url: "https://example.test/code-review" },
  license: "MIT",
  version: "1.2.0",
  capabilities: ["review-diff", "summarize-findings"],
  trustLevel: "reviewed",
  enabled: true,
  loadOrder: 20,
};

const researchSkill: SkillManifest = {
  id: "research",
  name: "Research",
  description: "Collects source metadata.",
  category: "research",
  source: { url: "https://example.test/research" },
  license: "Apache-2.0",
  version: "0.3.0",
  capabilities: ["search"],
  trustLevel: "untrusted",
  enabled: false,
  loadOrder: 5,
};

test("registers immutable metadata and lists by load order with stable ID tie-breaking", () => {
  const registry = new SkillRegistry();
  const stored = registry.register(codingSkill);
  registry.register(researchSkill);
  registry.register({ ...codingSkill, id: "code-audit", loadOrder: 20 });

  assert.deepEqual(registry.list().map(({ id }) => id), ["research", "code-audit", "code-review"]);
  assert.equal(registry.get("code-review"), stored);
  assert.equal(Object.isFrozen(stored), true);
  assert.equal(Object.isFrozen(stored.source), true);
  assert.equal(Object.isFrozen(stored.capabilities), true);
  assert.deepEqual(registry.list({ enabledOnly: true }).map(({ id }) => id), ["code-audit", "code-review"]);
  assert.deepEqual(registry.list({ category: "research" }).map(({ id }) => id), ["research"]);
});

test("enabling and disabling changes metadata only and unknown IDs fail explicitly", () => {
  const registry = new SkillRegistry([researchSkill]);
  assert.equal(registry.setEnabled("research", true).enabled, true);
  assert.equal(registry.list({ enabledOnly: true }).length, 1);
  assert.equal(registry.setEnabled("research", false).enabled, false);
  assert.equal(registry.list({ enabledOnly: true }).length, 0);
  assert.throws(() => registry.setEnabled("missing", true), /not registered/);
});

test("rejects duplicate IDs and malformed, incomplete, or unsafe-source metadata", () => {
  const registry = new SkillRegistry([codingSkill]);
  assert.throws(() => registry.register(codingSkill), /already registered/);
  assert.throws(() => new SkillRegistry([{ ...codingSkill, category: "unknown" } as unknown as SkillManifest]), /category/);
  assert.throws(() => new SkillRegistry([{ ...codingSkill, source: {} }]), /repository or URL/);
  assert.throws(() => new SkillRegistry([{ ...codingSkill, source: { url: "javascript:alert(1)" } }]), /HTTP or HTTPS/);
  assert.throws(() => new SkillRegistry([{ ...codingSkill, loadOrder: -1 }]), /load order/);
  assert.throws(() => new SkillRegistry([{ ...codingSkill, capabilities: [""] }]), /capabilities/);
});
