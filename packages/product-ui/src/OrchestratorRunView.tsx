import type { OrchestratorConsentState, OrchestratorMergeState, OrchestratorRunStatus, OrchestratorRunViewModel } from "./orchestrator-run-model";

export type OrchestratorRunViewLabels = {
	runDetails: string;
	runId: string;
	statuses: Record<OrchestratorRunStatus, string>;
	provider: string;
	model: string;
	tokenBudget: string;
	costBudget: string;
	consent: string;
	consentStates: Record<OrchestratorConsentState, string>;
	merge: string;
	mergeStates: Record<OrchestratorMergeState, string>;
	grantConsent: string;
	hold: string;
	resume: string;
	mergeAction: string;
	cancel: string;
};

export type OrchestratorRunViewActions = {
	onGrantConsent?: () => void;
	onHold?: () => void;
	onResume?: () => void;
	onMerge?: () => void;
	onCancel?: () => void;
};

export type OrchestratorRunViewProps = {
	run: OrchestratorRunViewModel;
	labels: OrchestratorRunViewLabels;
	actions?: OrchestratorRunViewActions;
	/** Explicit opt-in: omitted/false keeps the new surface entirely unrendered. */
	enabled?: boolean;
};

function finiteNonNegative(value: number): number {
	return Number.isFinite(value) ? Math.max(0, value) : 0;
}

function BudgetLine({ label, budget, currency = false }: { label: string; budget: { used: number; limit: number }; currency?: boolean }) {
	const used = finiteNonNegative(budget.used);
	const limit = finiteNonNegative(budget.limit);
	const progress = limit > 0 ? Math.min(100, Math.round((used / limit) * 100)) : 0;
	const format = (value: number) => currency
		? new Intl.NumberFormat(undefined, { style: "currency", currency: "USD", maximumFractionDigits: 2 }).format(value)
		: new Intl.NumberFormat().format(value);
	const unit = `${format(used)} / ${format(limit)}`;
	return (
		<div className="grid gap-1">
			<div className="flex items-center justify-between gap-3 text-xs">
				<span className="text-passive">{label}</span>
				<span>{unit}</span>
			</div>
			<div aria-label={label} aria-valuemax={100} aria-valuemin={0} aria-valuenow={progress} className="h-1.5 overflow-hidden rounded-full bg-muted" role="progressbar">
				<div className="h-full rounded-full bg-primary" style={{ width: `${progress}%` }} />
			</div>
		</div>
	);
}

const terminalStatuses = new Set<OrchestratorRunStatus>(["succeeded", "failed", "cancelled"]);

/** A portable, opt-in presentation only; hosts own persistence and actions. */
export function OrchestratorRunView({ run, labels, actions, enabled = false }: OrchestratorRunViewProps) {
	if (!enabled) return null;
	const canCancel = run.cancelAvailable && Boolean(actions?.onCancel) && !terminalStatuses.has(run.status);
	return (
		<section aria-label={labels.runDetails} className="grid gap-3 rounded-lg border border-border bg-card p-3 text-sm" data-testid="orchestrator-run-view">
			<header className="flex min-w-0 items-start justify-between gap-3">
				<div className="min-w-0">
					<p className="text-xs text-passive">{labels.runId}</p>
					<p className="truncate font-mono text-xs" title={run.runId}>{run.runId}</p>
				</div>
				<p aria-live="polite" className="shrink-0 rounded-full bg-muted px-2 py-1 text-xs font-medium" role="status">
					{labels.statuses[run.status]}
				</p>
			</header>

			<dl className="grid min-w-0 gap-x-4 gap-y-2 text-xs sm:grid-cols-2">
				{run.provider ? <div className="min-w-0"><dt className="text-passive">{labels.provider}</dt><dd className="truncate" title={run.provider}>{run.provider}</dd></div> : null}
				{run.model ? <div className="min-w-0"><dt className="text-passive">{labels.model}</dt><dd className="truncate" title={run.model}>{run.model}</dd></div> : null}
				<div><dt className="text-passive">{labels.consent}</dt><dd>{labels.consentStates[run.consent]}</dd></div>
				<div><dt className="text-passive">{labels.merge}</dt><dd>{labels.mergeStates[run.merge]}</dd></div>
			</dl>

			{run.tokenBudget || run.costBudgetUsd ? (
				<div className="grid gap-2">
					{run.tokenBudget ? <BudgetLine budget={run.tokenBudget} label={labels.tokenBudget} /> : null}
					{run.costBudgetUsd ? <BudgetLine budget={run.costBudgetUsd} currency label={labels.costBudget} /> : null}
				</div>
			) : null}

			{actions ? (
				<div className="flex flex-wrap gap-2 border-t border-border/70 pt-3">
					{run.consent === "required" && actions.onGrantConsent ? <button className="rounded-md border border-border px-2.5 py-1.5 text-xs" onClick={actions.onGrantConsent} type="button">{labels.grantConsent}</button> : null}
					{!terminalStatuses.has(run.status) && !run.held && actions.onHold ? <button className="rounded-md border border-border px-2.5 py-1.5 text-xs" onClick={actions.onHold} type="button">{labels.hold}</button> : null}
					{!terminalStatuses.has(run.status) && run.held && actions.onResume ? <button className="rounded-md border border-border px-2.5 py-1.5 text-xs" onClick={actions.onResume} type="button">{labels.resume}</button> : null}
					{run.merge === "ready" && actions.onMerge ? <button className="rounded-md border border-border px-2.5 py-1.5 text-xs" onClick={actions.onMerge} type="button">{labels.mergeAction}</button> : null}
					{canCancel && actions?.onCancel ? <button className="rounded-md border border-border px-2.5 py-1.5 text-xs text-error" onClick={actions.onCancel} type="button">{labels.cancel}</button> : null}
				</div>
			) : null}
		</section>
	);
}
