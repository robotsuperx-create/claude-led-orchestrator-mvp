/**
 * Metadata-only registry for skills. It never downloads, imports, or executes skill code.
 */

export const SKILL_CATEGORIES = [
  "automation",
  "coding",
  "data-analysis",
  "integration",
  "research",
  "writing",
  "other",
] as const;

export type SkillCategory = (typeof SKILL_CATEGORIES)[number];
export type SkillTrustLevel = "untrusted" | "reviewed" | "trusted";

/** Links describing where a skill comes from; these values are never fetched by this package. */
export interface SkillSource {
  readonly repository?: string;
  readonly url?: string;
}

/** Descriptive metadata only; this manifest does not contain or authorize executable code. */
export interface SkillManifest {
  readonly id: string;
  readonly name: string;
  readonly description: string;
  readonly category: SkillCategory;
  readonly source: SkillSource;
  readonly license: string;
  readonly version: string;
  readonly capabilities: readonly string[];
  readonly trustLevel: SkillTrustLevel;
  readonly enabled: boolean;
  /** Lower values sort earlier when listing manifests for a caller-defined load sequence. */
  readonly loadOrder: number;
}

export interface SkillListOptions {
  readonly enabledOnly?: boolean;
  readonly category?: SkillCategory;
}

const CATEGORY_SET: ReadonlySet<string> = new Set(SKILL_CATEGORIES);
const TRUST_LEVELS: ReadonlySet<string> = new Set(["untrusted", "reviewed", "trusted"]);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value.trim().length > 0;
}

function validateManifest(value: unknown): asserts value is SkillManifest {
  if (!isRecord(value)) throw new TypeError("Skill manifest must be an object.");
  if (!isNonEmptyString(value.id)) throw new TypeError("Skill manifest id must be a non-empty string.");
  if (!isNonEmptyString(value.name)) throw new TypeError("Skill manifest name must be a non-empty string.");
  if (!isNonEmptyString(value.description)) throw new TypeError("Skill manifest description must be a non-empty string.");
  if (typeof value.category !== "string" || !CATEGORY_SET.has(value.category)) {
    throw new TypeError("Skill manifest category is unsupported.");
  }
  if (!isRecord(value.source)) throw new TypeError("Skill manifest source must be an object.");

  const repository = value.source.repository;
  const url = value.source.url;
  if (repository !== undefined && !isNonEmptyString(repository)) {
    throw new TypeError("Skill source repository must be a non-empty string when provided.");
  }
  if (url !== undefined) {
    if (!isNonEmptyString(url)) throw new TypeError("Skill source URL must be a non-empty string when provided.");
    let parsed: URL;
    try {
      parsed = new URL(url);
    } catch {
      throw new TypeError("Skill source URL must be a valid HTTP or HTTPS URL.");
    }
    if (parsed.protocol !== "https:" && parsed.protocol !== "http:") {
      throw new TypeError("Skill source URL must use HTTP or HTTPS.");
    }
  }
  if (repository === undefined && url === undefined) {
    throw new TypeError("Skill source must include a repository or URL.");
  }
  if (!isNonEmptyString(value.license)) throw new TypeError("Skill manifest license must be a non-empty string.");
  if (!isNonEmptyString(value.version)) throw new TypeError("Skill manifest version must be a non-empty string.");
  if (!Array.isArray(value.capabilities) || !value.capabilities.every(isNonEmptyString)) {
    throw new TypeError("Skill manifest capabilities must be an array of non-empty strings.");
  }
  if (typeof value.trustLevel !== "string" || !TRUST_LEVELS.has(value.trustLevel)) {
    throw new TypeError("Skill manifest trust level is unsupported.");
  }
  if (typeof value.enabled !== "boolean") throw new TypeError("Skill manifest enabled must be a boolean.");
  if (!Number.isSafeInteger(value.loadOrder) || (value.loadOrder as number) < 0) {
    throw new TypeError("Skill manifest load order must be a non-negative safe integer.");
  }
}

function snapshot(manifest: SkillManifest): SkillManifest {
  const source: SkillSource = Object.freeze({ ...manifest.source });
  return Object.freeze({
    ...manifest,
    source,
    capabilities: Object.freeze([...manifest.capabilities]),
  });
}

/**
 * Stores and orders skill metadata only. Enabling a skill changes metadata; it never loads code.
 */
export class SkillRegistry {
  private readonly manifests = new Map<string, SkillManifest>();

  constructor(manifests: readonly SkillManifest[] = []) {
    for (const manifest of manifests) this.register(manifest);
  }

  /** Validate and add one manifest; duplicate IDs are rejected rather than silently replaced. */
  register(manifest: SkillManifest): SkillManifest {
    validateManifest(manifest);
    if (this.manifests.has(manifest.id)) {
      throw new Error(`Skill '${manifest.id}' is already registered.`);
    }
    const stored = snapshot(manifest);
    this.manifests.set(stored.id, stored);
    return stored;
  }

  get(id: string): SkillManifest | undefined {
    return this.manifests.get(id);
  }

  /** Return a stable metadata listing ordered by loadOrder and then ID. */
  list(options: SkillListOptions = {}): readonly SkillManifest[] {
    const entries = [...this.manifests.values()].filter((manifest) =>
      (options.enabledOnly !== true || manifest.enabled) &&
      (options.category === undefined || manifest.category === options.category)
    );
    entries.sort((left, right) => left.loadOrder - right.loadOrder || left.id.localeCompare(right.id));
    return Object.freeze(entries);
  }

  /** Enable or disable metadata for a registered skill; no code is loaded or executed. */
  setEnabled(id: string, enabled: boolean): SkillManifest {
    if (typeof enabled !== "boolean") throw new TypeError("Enabled state must be a boolean.");
    const current = this.manifests.get(id);
    if (current === undefined) throw new Error(`Skill '${id}' is not registered.`);
    const updated = snapshot({ ...current, enabled });
    this.manifests.set(id, updated);
    return updated;
  }
}
