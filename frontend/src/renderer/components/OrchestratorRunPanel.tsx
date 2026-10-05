import { useState, type FormEvent } from "react";
import {
	OrchestratorRunView,
	type OrchestratorRunViewLabels,
	type OrchestratorRunViewModel,
} from "@aoagents/product-ui";
import {
	useOrchestratorRun,
	type OrchestratorRunClient,
	type OrchestratorRunError,
} from "../hooks/useOrchestratorRun";

/** Copy-only safe display fields supplied by a host with a verified contract. */
export type OrchestratorRunPanelPresentation = Partial<Pick<
	OrchestratorRunViewModel,
	"provider" | "model" | "tokenBudget" | "costBudgetUsd" | "held" | "merge"
>>;

export type OrchestratorRunPanelLabels = {
	run: OrchestratorRunViewLabels;
	task: string;
	taskPlaceholder: string;
	consentStatement: string;
	consentCheckbox: string;
	start: string;
	starting: string;
	errorMessages: Record<OrchestratorRunError, string>;
};

export type OrchestratorRunPanelProps = {
	client: OrchestratorRunClient;
	labels: OrchestratorRunPanelLabels;
	/** Explicit host feature flag. Omitted/false keeps the screen unrendered. */
	enabled?: boolean;
	/** Cancellation remains off unless the injected host contract explicitly supports it. */
	cancelEnabled?: boolean;
	pollIntervalMs?: number;
	presentation?: OrchestratorRunPanelPresentation;
	/** Host callbacks require verified hold/resume/merge operations. */
	actions?: {
		onHold?: () => void;
		onResume?: () => void;
		onMerge?: () => void;
	};
};

const activeStatuses = new Set(["queued", "running", "awaiting_consent", "on_hold"]);

/**
 * Experimental renderer screen. It owns consent and form state, while all
 * network operations are made only through the host-injected client. The
 * default-off gate is deliberately repeated here even though the leaf view is
 * also opt-in.
 */
export function OrchestratorRunPanel({
	client,
	labels,
	enabled = false,
	cancelEnabled = false,
	pollIntervalMs,
	presentation,
	actions,
}: OrchestratorRunPanelProps) {
	const [task, setTask] = useState("");
	const [consentGranted, setConsentGranted] = useState(false);
	const [consentedRunId, setConsentedRunId] = useState<string | null>(null);
	const { run, isStarting, error, startRun, cancelRun } = useOrchestratorRun({
		client,
		enabled,
		consentGranted,
		cancelEnabled,
		...(pollIntervalMs === undefined ? {} : { pollIntervalMs }),
	});

	if (!enabled) return null;

	const active = run !== null && activeStatuses.has(run.status);
	const viewRun = run ? {
		...run,
		provider: presentation?.provider,
		model: presentation?.model,
		tokenBudget: presentation?.tokenBudget,
		costBudgetUsd: presentation?.costBudgetUsd,
		held: presentation?.held ?? run.held,
		merge: presentation?.merge ?? run.merge,
		consent: consentedRunId === run.runId ? "granted" as const : "required" as const,
	} : null;

	const submit = async (event: FormEvent<HTMLFormElement>) => {
		event.preventDefault();
		if (!consentGranted || active || isStarting) return;
		const started = await startRun({ task });
		setConsentGranted(false);
		setConsentedRunId(started?.runId ?? null);
	};

	return (
		<div className="grid gap-4">
			<form className="grid gap-3 rounded-lg border border-border bg-card p-3" onSubmit={(event) => void submit(event)}>
				<label className="grid gap-1 text-sm">
					<span>{labels.task}</span>
					<textarea
						className="min-h-24 rounded-md border border-border bg-background p-2 text-sm"
						maxLength={8192}
						placeholder={labels.taskPlaceholder}
						value={task}
						onChange={(event) => {
							setTask(event.currentTarget.value);
							setConsentGranted(false);
						}}
					/>
				</label>
				<label className="flex items-start gap-2 text-sm">
					<input
						checked={consentGranted}
						className="mt-0.5"
						type="checkbox"
						onChange={(event) => setConsentGranted(event.currentTarget.checked)}
					/>
					<span>{labels.consentCheckbox}</span>
				</label>
				<p className="text-xs text-passive">{labels.consentStatement}</p>
				<button
					className="w-fit rounded-md border border-border px-3 py-1.5 text-sm disabled:cursor-not-allowed disabled:opacity-50"
					disabled={!consentGranted || !task.trim() || active || isStarting}
					type="submit"
				>
					{isStarting ? labels.starting : labels.start}
				</button>
			</form>

			{error ? <p className="text-sm text-error" role="alert">{labels.errorMessages[error]}</p> : null}

			{viewRun ? (
				<OrchestratorRunView
					actions={{
						onHold: actions?.onHold,
						onResume: actions?.onResume,
						onMerge: actions?.onMerge,
						...(cancelEnabled ? { onCancel: () => void cancelRun() } : {}),
					}}
					enabled={enabled}
					labels={labels.run}
					run={viewRun}
				/>
			) : null}
		</div>
	);
}
