# Orchestrator run UI integration point

The new `@aoagents/product-ui` `OrchestratorRunView` is a presentation-only surface and is **not mounted in the renderer**. It returns no UI unless its host passes `enabled={true}`; omit that prop in ordinary application paths. It accepts an allowlisted `OrchestratorRunViewModel`, translated labels, and optional host callbacks. It contains no API client, persistence, routing, or credential fields.

## Current API boundary

There is no generated orchestrator run/status/cancel route in `frontend/src/api/schema.ts`. The existing renderer consumes HTTP through `openapi-fetch` clients in `frontend/src/renderer/lib/api-client.ts` and `frontend/src/renderer/lib/host-clients.ts`, with generated `components["schemas"]` types and React Query hooks (for example, `frontend/src/renderer/hooks/useSessionUsage.ts`). The current backend proposal documents that no route is registered, status/cancel lack a run-registry/cancellation boundary, and the generated OpenAPI must be produced from the canonical code-first source rather than hand-authored.

Do not invent a schema or connect this component to the existing `/api/v1/orchestrators` session lifecycle: that API is not a run-management API. The renderer has an opt-in `OrchestratorRunPanel` wrapper at `frontend/src/renderer/components/OrchestratorRunPanel.tsx`; its `enabled` prop defaults to `false`, and its injected `OrchestratorRunClient` is used by `useOrchestratorRun` for start, status polling, and optionally cancel. It presents an unchecked consent control before start, consumes consent after each attempt, and adapts only the hook's allowlisted run model into the product-ui view. Mount it from a host screen only behind the host's default-off experiment flag and only after supplying a verified local client.

The wrapper intentionally is not mounted in the default shell yet: the generated OpenAPI has no run/status/cancel route, and the current backend contract does not yet define hold/resume/merge operations. Cancellation defaults off; hold/resume/merge remain optional host callbacks and are rendered only when the host supplies them and the adapted presentation state allows the action. A host may supply only the typed `presentation` fields (provider/model/budgets/held/merge), never a wire response. Once the backend registers and documents safe operations, generate the schema through the repository's `api:ts` flow, bind the generated/verified client at the host boundary, and enable the panel behind the experiment flag. The focused wrapper test covers the safe wiring without enabling the production surface.

## Data and security rules

- Map only run ID/state, model/provider identifiers, token and USD budget totals, consent state, hold state, merge state, and whether cancel is currently allowed.
- Never pass a wire object directly. Never map or render API keys, OAuth/access/refresh tokens, authorization headers, task text, raw worker output, or raw internal errors.
- Keep labels host-translated; the package does not import renderer i18n or API types.
- Do not show cancel unless the server contract exposes cancellation and the current run is cancellable. Consent/hold/merge buttons require explicit host callbacks and call no remote service themselves.
