// Baked default Sentry DSN for the desktop surfaces (renderer, Electron main,
// and the Go daemon spawned by the app), mirroring how DEFAULT_POSTHOG_PROJECT_KEY
// is committed in posthog-config.ts. A client DSN is a write-only ingest
// endpoint, so it is safe to ship in the client the same way the PostHog project
// key already is; volume is bounded by the project's Sentry-side spike protection
// and rate limit, not by anything secret here.
//
// Overridable per environment: the renderer reads VITE_AO_SENTRY_DSN, the main
// process and daemon read AO_SENTRY_DSN, and both fall back to this constant.
// Mobile has its own project and constant (see packages/mobile), because the
// Expo bundle cannot reach frontend/src.
//
// Empty for X-ROBOT-AI: crash reports go nowhere unless its owner sets a DSN.
export const DEFAULT_SENTRY_DSN = "";
