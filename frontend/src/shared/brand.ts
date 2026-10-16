/**
 * Product identity. X-ROBOT-AI is built on the open-source Agent Orchestrator
 * (Apache-2.0); keep that attribution wherever the product describes itself.
 */
export const BRAND_NAME = "X-ROBOT-AI";
export const BRAND_ATTRIBUTION = "Built on Agent Orchestrator (Apache-2.0)";

/**
 * The product's own GitHub repository. Releases, the update feed, the version
 * floor, and problem reports all point here, never at the upstream project,
 * so an installed X-ROBOT-AI can never update itself into Agent Orchestrator.
 */
export const PRODUCT_REPO = { owner: "robotsuperx-create", repo: "claude-led-orchestrator-mvp" } as const;
export const PRODUCT_REPO_SLUG = `${PRODUCT_REPO.owner}/${PRODUCT_REPO.repo}`;
