import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { ScrollArea } from "radix-ui";
import { useTranslation } from "react-i18next";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { Check, ChevronRight, Copy, LoaderCircle, Plus, RefreshCw, TriangleAlert, Workflow, X } from "lucide-react";
import {
	CLAUDE_ORCHESTRATOR_ACTIVE_STAGES,
	CLAUDE_ORCHESTRATOR_MAX_RETRIES,
	CLAUDE_ORCHESTRATOR_MAX_TASK_BYTES,
	isTerminalClaudeOrchestratorState,
	type ClaudeOrchestratorErrorCode,
	type ClaudeOrchestratorInfo,
	type ClaudeOrchestratorRun,
	type ClaudeOrchestratorStage,
} from "../../shared/claude-orchestrator";
import {
	claudeOrchestratorErrorCode,
	useCancelClaudeOrchestratorRun,
	useClaudeOrchestratorInfo,
	useClaudeOrchestratorRuns,
	useStartClaudeOrchestratorRun,
} from "../hooks/useClaudeOrchestrator";
import { aoBridge } from "../lib/bridge";
import { hidesShellTopbar } from "../lib/platform";
import {
	centeredOnboardingDialogClass,
	onboardingFieldErrorClass,
	onboardingFieldHintClass,
	onboardingFooterActionsEndClass,
	onboardingFormLabelClass,
} from "../lib/onboarding-ui";
import { cn } from "../lib/utils";
import { ConfirmDialog } from "./ConfirmDialog";
import { TopbarButton, topbarProjectLabelClass } from "./TopbarButton";
import { Button } from "./ui/button";
import { Checkbox } from "./ui/checkbox";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogTitle } from "./ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";

const TASK_FIELD_ID = "claude-orchestrator-task";
const RETRIES_FIELD_ID = "claude-orchestrator-retries";
const CONSENT_FIELD_ID = "claude-orchestrator-consent";
// Technical values rendered verbatim (not translatable copy).
const RUNBOOK_PATH = "docs/claude-led-multimodel/08-runbook.md";
const reviewCommand = (branch: string) => `git diff HEAD...${branch}`;

// Product names for the daemon's provider IDs; unknown IDs show as-is.
const providerNames: Record<string, string> = { claude: "Claude", deepseek: "DeepSeek" };
const providerName = (id: string) => providerNames[id] ?? id;

function displayTime(value: string | undefined, locale: string | undefined) {
	if (!value) return "—";
	return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

function stageKey(run: ClaudeOrchestratorRun) {
	if (run.state === "completed") return run.recommendation === "merge" ? "orchestrator.state.readyToMerge" as const : "orchestrator.state.completed" as const;
	return `orchestrator.state.${run.state}` as const;
}

/** One fixed status slot: a spinner while working, otherwise a semantic dot. */
function RunStatusGlyph({ run }: { run: ClaudeOrchestratorRun }) {
	const { t } = useTranslation();
	const label = t(stageKey(run));
	if (!isTerminalClaudeOrchestratorState(run.state)) {
		const tone = run.state === "validating" ? "text-status-validating" : "text-status-working";
		return <LoaderCircle role="img" aria-label={label} className={cn("size-icon-sm shrink-0 animate-spin motion-reduce:animate-none", tone)} />;
	}
	const tone = run.state === "completed"
		? "bg-status-ready"
		: run.state === "held"
			? "bg-status-in-review"
			: run.state === "failed"
				? "bg-status-exited"
				: "bg-muted-foreground/50";
	return <span role="img" aria-label={label} className={cn("inline-block size-2 shrink-0 rounded-full", tone)} />;
}

export function ClaudeOrchestratorView() {
	const { t } = useTranslation();
	const showPageTitle = hidesShellTopbar();
	const info = useClaudeOrchestratorInfo();
	const enabled = info.data?.enabled === true;
	const runs = useClaudeOrchestratorRuns(enabled);
	const [createOpen, setCreateOpen] = useState(false);
	const [expanded, setExpanded] = useState<string | null>(null);
	const runList = runs.data ?? [];
	const activeCount = runList.filter((run) => !isTerminalClaudeOrchestratorState(run.state)).length;
	const atCapacity = enabled && info.data !== undefined && activeCount >= info.data.maxActiveRuns;
	const infoError = claudeOrchestratorErrorCode(info.error);
	const runsError = claudeOrchestratorErrorCode(runs.error);

	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background">
			<header className="workspace-topbar-container center-panel-titlebar flex h-toolbar shrink-0 items-center justify-between gap-3 border-b border-border-strong pr-2">
				<div className="flex min-w-0 items-center gap-2">
					<h1 className={showPageTitle ? cn(topbarProjectLabelClass, "inline-flex items-center gap-1.5") : "sr-only"}>
						{showPageTitle ? <Workflow className="size-icon-md" aria-hidden="true" /> : null}
						{t("orchestrator.title")}
					</h1>
					{!showPageTitle ? <p className="truncate text-xs text-muted-foreground">{t("orchestrator.description")}</p> : null}
				</div>
				<div className="workspace-topbar-actions flex shrink-0 items-center">
					<TopbarButton
						variant="primary"
						className="topbar-control--labeled"
						disabled={!enabled || atCapacity}
						title={atCapacity ? t("orchestrator.atCapacity", { limit: info.data?.maxActiveRuns ?? 0 }) : undefined}
						onClick={() => setCreateOpen(true)}
					>
						<Plus className="size-icon-md" aria-hidden="true" />
						{t("orchestrator.new")}
					</TopbarButton>
				</div>
			</header>
			<ScrollArea.Root asChild type="hover" scrollHideDelay={500}>
				<main className="relative min-h-0 flex-1 overflow-hidden">
					<ScrollArea.Viewport className="h-full w-full overscroll-contain focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60" tabIndex={0}>
						<div className="p-4">
							{info.isLoading ? <p role="status" className="px-3 py-8 text-center text-sm text-muted-foreground">{t("orchestrator.loading")}</p> : null}
							{infoError ? <DaemonUnavailable onRetry={() => void info.refetch()} /> : null}
							{info.data && !enabled ? <OrchestratorDisabled /> : null}
							{enabled && info.data ? <ConfigurationLine info={info.data} /> : null}
							{enabled && runsError ? (
								<p role="alert" className="mb-3 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">{t(`orchestrator.error.${runsError}`)}</p>
							) : null}
							{enabled && runs.isLoading ? <p role="status" className="px-3 py-8 text-center text-sm text-muted-foreground">{t("orchestrator.runs.loading")}</p> : null}
							{enabled && !runs.isLoading && !runsError && runList.length === 0 ? <EmptyRuns onCreate={() => setCreateOpen(true)} /> : null}
							{enabled && runList.length > 0 ? (
								<div className="overflow-hidden rounded-lg border border-border bg-background">
									<div className="hidden grid-cols-[minmax(0,1fr)_minmax(0,240px)_160px] items-center gap-4 border-b border-border bg-muted/20 px-4 py-2 text-[11px] text-muted-foreground lg:grid" aria-hidden="true">
										<span>{t("orchestrator.column.task")}</span>
										<span>{t("orchestrator.column.branch")}</span>
										<span>{t("orchestrator.column.updated")}</span>
									</div>
									{runList.map((run) => (
										<RunRow
											key={run.runId}
											run={run}
											expanded={expanded === run.runId}
											onExpand={() => setExpanded(expanded === run.runId ? null : run.runId)}
										/>
									))}
								</div>
							) : null}
						</div>
					</ScrollArea.Viewport>
					<ScrollArea.Scrollbar orientation="vertical" className="z-10 flex w-2 touch-none select-none p-0.5">
						<ScrollArea.Thumb className="relative flex-1 rounded-full bg-[color-mix(in_srgb,var(--color-scrollbar)_42%,transparent)] hover:bg-[color-mix(in_srgb,var(--color-scrollbar)_56%,transparent)]" />
					</ScrollArea.Scrollbar>
				</main>
			</ScrollArea.Root>
			{info.data && enabled ? (
				<NewRunDialog
					open={createOpen}
					info={info.data}
					onOpenChange={setCreateOpen}
					onStarted={(runId) => {
						setCreateOpen(false);
						setExpanded(runId);
					}}
				/>
			) : null}
		</div>
	);
}

function ConfigurationLine({ info }: { info: ClaudeOrchestratorInfo }) {
	const { t } = useTranslation();
	return (
		<p className="mb-3 flex flex-wrap items-center gap-x-1.5 gap-y-1 text-xs text-muted-foreground" data-testid="claude-orchestrator-configuration">
			<span className="font-medium text-foreground">{info.repository}</span>
			<span aria-hidden="true">·</span>
			<span>{t("orchestrator.config.planner")} <span className="font-mono text-[11px]">{info.plannerModel}</span></span>
			<span aria-hidden="true">·</span>
			<span>{t("orchestrator.config.worker", { provider: providerName(info.workerProvider) })} <span className="font-mono text-[11px]">{info.workerModel}</span></span>
			<span aria-hidden="true">·</span>
			<span>{t(info.sandboxed ? "orchestrator.config.sandboxOn" : "orchestrator.config.sandboxOff")}</span>
		</p>
	);
}

function DaemonUnavailable({ onRetry }: { onRetry: () => void }) {
	const { t } = useTranslation();
	return (
		<div role="alert" className="mb-3 flex items-center justify-between gap-3 rounded-md bg-destructive/10 px-3 py-2 text-xs text-destructive">
			<span className="flex items-center gap-2"><TriangleAlert className="size-3.5 shrink-0" aria-hidden="true" />{t("orchestrator.error.daemon_unavailable")}</span>
			<Button variant="ghost" size="sm" onClick={onRetry}><RefreshCw className="size-3.5" aria-hidden="true" />{t("orchestrator.retry")}</Button>
		</div>
	);
}

function OrchestratorDisabled() {
	const { t } = useTranslation();
	return (
		<div className="grid min-h-64 place-items-center p-8 text-center" data-testid="claude-orchestrator-disabled">
			<div className="max-w-md">
				<Workflow className="mx-auto mb-3 size-8 text-muted-foreground" aria-hidden="true" />
				<h2 className="text-[17px] font-medium">{t("orchestrator.disabled.title")}</h2>
				<p className="mt-1 text-sm text-muted-foreground">{t("orchestrator.disabled.description")}</p>
				<p className="mt-3 font-mono text-[11px] text-muted-foreground">{RUNBOOK_PATH}</p>
			</div>
		</div>
	);
}

function EmptyRuns({ onCreate }: { onCreate: () => void }) {
	const { t } = useTranslation();
	return (
		<div className="grid min-h-64 place-items-center p-8 text-center">
			<div>
				<Workflow className="mx-auto mb-3 size-8 text-muted-foreground" aria-hidden="true" />
				<h2 className="text-[17px] font-medium">{t("orchestrator.empty.title")}</h2>
				<p className="mt-1 text-sm text-muted-foreground">{t("orchestrator.empty.description")}</p>
				<Button className="mt-4" onClick={onCreate}>{t("orchestrator.empty.action")}</Button>
			</div>
		</div>
	);
}

function RunRow({ run, expanded, onExpand }: { run: ClaudeOrchestratorRun; expanded: boolean; onExpand: () => void }) {
	const { t, i18n } = useTranslation();
	const reduceMotion = useReducedMotion();
	const detailsId = `claude-orchestrator-run-${run.runId}`;
	const title = run.title || t("orchestrator.untitled");
	return (
		<article className="group border-b border-border last:border-b-0" data-testid="claude-orchestrator-run" data-state={run.state}>
			<button
				type="button"
				aria-expanded={expanded}
				aria-controls={detailsId}
				aria-label={t(expanded ? "orchestrator.run.hide" : "orchestrator.run.show", { title })}
				onClick={onExpand}
				className={cn(
					"grid w-full grid-cols-[minmax(0,1fr)] items-center gap-x-4 gap-y-1 px-4 py-3 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60 motion-reduce:transition-none lg:grid-cols-[minmax(0,1fr)_minmax(0,240px)_160px]",
					expanded ? "bg-muted/30" : "hover:bg-muted/20",
				)}
			>
				<div className="flex min-w-0 items-center gap-2.5">
					<ChevronRight aria-hidden="true" className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform duration-150 motion-reduce:transition-none", expanded && "rotate-90")} />
					<span className="grid size-4 shrink-0 place-items-center"><RunStatusGlyph run={run} /></span>
					<div className="min-w-0">
						<h2 className="truncate text-[13px] font-medium leading-5 text-foreground" title={title}>{title}</h2>
						<p className="truncate text-[11px] leading-5 text-muted-foreground">{t(stageKey(run))}</p>
					</div>
				</div>
				<span className="truncate pl-11 font-mono text-[11px] text-muted-foreground lg:pl-0" title={run.branch}>{run.branch ?? "—"}</span>
				<span className="pl-11 text-xs tabular-nums text-muted-foreground lg:pl-0">{displayTime(run.updatedAt ?? run.createdAt, i18n.resolvedLanguage)}</span>
			</button>
			<AnimatePresence initial={false}>
				{expanded ? (
					<motion.div
						id={detailsId}
						initial={{ height: 0, opacity: 0 }}
						animate={{ height: "auto", opacity: 1 }}
						exit={{ height: 0, opacity: 0 }}
						transition={{ duration: reduceMotion ? 0 : 0.18, ease: "easeOut" }}
						className="overflow-hidden"
					>
						<RunDetails run={run} />
					</motion.div>
				) : null}
			</AnimatePresence>
		</article>
	);
}

const stageLabelKey = {
	planning: "orchestrator.stage.planning",
	executing: "orchestrator.stage.executing",
	validating: "orchestrator.stage.validating",
	reviewing: "orchestrator.stage.reviewing",
} as const satisfies Record<ClaudeOrchestratorStage, string>;

const retryLabelKeys = ["orchestrator.form.retry0", "orchestrator.form.retry1", "orchestrator.form.retry2", "orchestrator.form.retry3"] as const;

function StageTimeline({ run }: { run: ClaudeOrchestratorRun }) {
	const { t } = useTranslation();
	const currentIndex = CLAUDE_ORCHESTRATOR_ACTIVE_STAGES.indexOf(run.state as ClaudeOrchestratorStage);
	return (
		<ol className="mb-4 flex flex-wrap items-center gap-x-2 gap-y-2 text-xs" aria-label={t("orchestrator.stage.label")}>
			{CLAUDE_ORCHESTRATOR_ACTIVE_STAGES.map((stage, index) => {
				const done = currentIndex > index;
				const current = currentIndex === index;
				return (
					<li key={stage} className="flex items-center gap-2" aria-current={current ? "step" : undefined}>
						{index > 0 ? <span aria-hidden="true" className={cn("h-px w-6", done || current ? "bg-status-working/60" : "bg-border")} /> : null}
						<span className={cn("flex items-center gap-1.5", current ? "font-medium text-foreground" : done ? "text-foreground" : "text-muted-foreground")}>
							{done ? <Check className="size-3.5 text-status-ready" aria-hidden="true" /> : current ? <LoaderCircle className="size-3.5 animate-spin text-status-working motion-reduce:animate-none" aria-hidden="true" /> : <span aria-hidden="true" className="inline-block size-1.5 rounded-full bg-muted-foreground/40" />}
							{t(stageLabelKey[stage])}
						</span>
					</li>
				);
			})}
		</ol>
	);
}

function outcomeKey(run: ClaudeOrchestratorRun) {
	switch (run.state) {
		case "completed":
			if (!run.commit) return "orchestrator.outcome.noChanges" as const;
			return run.recommendation === "merge" ? "orchestrator.outcome.merge" as const : "orchestrator.outcome.completed" as const;
		case "held":
			return "orchestrator.outcome.held" as const;
		case "failed":
			return "orchestrator.outcome.failed" as const;
		case "canceled":
			return "orchestrator.outcome.canceled" as const;
		default:
			return null;
	}
}

function RunDetails({ run }: { run: ClaudeOrchestratorRun }) {
	const { t } = useTranslation();
	const cancel = useCancelClaudeOrchestratorRun();
	const [confirmCancel, setConfirmCancel] = useState(false);
	const active = !isTerminalClaudeOrchestratorState(run.state);
	const outcome = outcomeKey(run);
	const cancelError = claudeOrchestratorErrorCode(cancel.error);
	return (
		<div className="border-t border-border bg-muted/10 px-4 py-4 pl-[3.25rem]">
			{active ? <StageTimeline run={run} /> : null}
			{outcome ? <p className="mb-4 max-w-prose text-[13px] leading-5 text-foreground" data-testid="claude-orchestrator-outcome">{t(outcome)}</p> : null}
			<dl className="grid max-w-xl grid-cols-[96px_minmax(0,1fr)] items-center gap-x-3 gap-y-2 text-xs">
				{run.branch ? (
					<>
						<dt className="text-muted-foreground">{t("orchestrator.detail.branch")}</dt>
						<dd className="flex min-w-0 items-center gap-1"><span className="truncate font-mono text-[11px]" title={run.branch}>{run.branch}</span><CopyButton value={run.branch} label={t("orchestrator.copy.branch")} /></dd>
					</>
				) : null}
				{run.commit ? (
					<>
						<dt className="text-muted-foreground">{t("orchestrator.detail.commit")}</dt>
						<dd className="flex min-w-0 items-center gap-1"><span className="font-mono text-[11px]" title={run.commit}>{run.commit.slice(0, 10)}</span><CopyButton value={run.commit} label={t("orchestrator.copy.commit")} /></dd>
					</>
				) : null}
				{run.branch && run.commit ? (
					<>
						<dt className="text-muted-foreground">{t("orchestrator.detail.review")}</dt>
						<dd className="flex min-w-0 items-center gap-1"><span className="truncate font-mono text-[11px]">{reviewCommand(run.branch)}</span><CopyButton value={reviewCommand(run.branch)} label={t("orchestrator.copy.review")} /></dd>
					</>
				) : null}
				<dt className="text-muted-foreground">{t("orchestrator.detail.runId")}</dt>
				<dd className="truncate font-mono text-[11px] text-muted-foreground" title={run.runId}>{run.runId}</dd>
			</dl>
			{active ? (
				<div className="mt-4 flex items-center gap-2">
					<Button variant="outline" size="sm" className="text-destructive hover:text-destructive" disabled={cancel.isPending} onClick={() => setConfirmCancel(true)}>
						<X className="size-3.5" aria-hidden="true" />{t("orchestrator.cancel.action")}
					</Button>
				</div>
			) : null}
			<ConfirmDialog
				open={confirmCancel}
				title={t("orchestrator.cancel.title")}
				description={t("orchestrator.cancel.description")}
				confirmLabel={t("orchestrator.cancel.confirm")}
				destructive
				busy={cancel.isPending}
				error={cancelError ? t(`orchestrator.error.${cancelError}`) : null}
				onOpenChange={(open) => { setConfirmCancel(open); if (!open) cancel.reset(); }}
				onConfirm={() => cancel.mutate(run.runId, { onSuccess: () => setConfirmCancel(false) })}
			/>
		</div>
	);
}

function CopyButton({ value, label }: { value: string; label: string }) {
	const { t } = useTranslation();
	const [copied, setCopied] = useState(false);
	const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
	useEffect(() => () => { if (timer.current) clearTimeout(timer.current); }, []);
	return (
		<Button
			variant="ghost"
			size="icon-sm"
			className="size-6 shrink-0 text-muted-foreground"
			aria-label={copied ? t("orchestrator.copy.done") : label}
			title={copied ? t("orchestrator.copy.done") : label}
			onClick={() => {
				void aoBridge.clipboard.writeText(value).then(() => {
					setCopied(true);
					if (timer.current) clearTimeout(timer.current);
					timer.current = setTimeout(() => setCopied(false), 1_500);
				});
			}}
		>
			{copied ? <Check className="size-3.5 text-status-ready" aria-hidden="true" /> : <Copy className="size-3.5" aria-hidden="true" />}
		</Button>
	);
}

function NewRunDialog({ open, info, onOpenChange, onStarted }: { open: boolean; info: ClaudeOrchestratorInfo; onOpenChange: (open: boolean) => void; onStarted: (runId: string) => void }) {
	const { t } = useTranslation();
	const start = useStartClaudeOrchestratorRun();
	const [task, setTask] = useState("");
	const [retries, setRetries] = useState("1");
	const [consent, setConsent] = useState(false);
	const [taskError, setTaskError] = useState<string | null>(null);
	const busy = start.isPending;

	useEffect(() => {
		if (!open) return;
		setTask("");
		setRetries("1");
		setConsent(false);
		setTaskError(null);
		start.reset();
		// Reset only when the dialog opens.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [open]);

	const taskBytes = useMemo(() => new TextEncoder().encode(task).byteLength, [task]);
	const startError: ClaudeOrchestratorErrorCode | null = claudeOrchestratorErrorCode(start.error);

	const submit = (event: FormEvent<HTMLFormElement>) => {
		event.preventDefault();
		if (busy) return;
		if (task.trim() === "") {
			setTaskError(t("orchestrator.form.taskRequired"));
			return;
		}
		if (taskBytes > CLAUDE_ORCHESTRATOR_MAX_TASK_BYTES) {
			setTaskError(t("orchestrator.form.taskTooLong"));
			return;
		}
		if (!consent) return;
		start.mutate({ task, maxRetries: Number(retries) }, { onSuccess: (run) => onStarted(run.runId) });
	};

	return (
		<Dialog open={open} onOpenChange={(next) => { if (!busy) onOpenChange(next); }}>
			<DialogContent showCloseButton={false} className={centeredOnboardingDialogClass}>
				<DialogClose asChild>
					<button type="button" disabled={busy} className="settings-dialog-close-button settings-close-button" aria-label={t("orchestrator.form.close")}>
						<X className="size-4" aria-hidden="true" />
					</button>
				</DialogClose>
				<DialogTitle className="settings-dialog-title px-4 pr-12 pt-3">{t("orchestrator.form.title")}</DialogTitle>
				<DialogDescription className="px-4 pr-12 pt-1 text-[13px] leading-5 text-muted-foreground">
					{t("orchestrator.form.description", { repository: info.repository, planner: info.plannerModel, worker: info.workerModel })}
				</DialogDescription>
				<form className="flex min-h-0 flex-1 flex-col" noValidate onSubmit={submit}>
					<div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-1 pt-4">
						<div className="flex flex-col gap-2">
							<label htmlFor={TASK_FIELD_ID} className={onboardingFormLabelClass}>{t("orchestrator.form.task")}</label>
							<textarea
								id={TASK_FIELD_ID}
								autoFocus
								value={task}
								disabled={busy}
								aria-invalid={Boolean(taskError) || undefined}
								aria-describedby={taskError ? `${TASK_FIELD_ID}-error` : undefined}
								placeholder={t("orchestrator.form.taskPlaceholder")}
								onChange={(event) => { setTask(event.target.value); setTaskError(null); }}
								className="min-h-32 w-full resize-y rounded-md border border-transparent bg-input/50 px-3 py-2 text-[13px] leading-5 outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/30 aria-invalid:border-destructive"
							/>
							{taskError ? <p id={`${TASK_FIELD_ID}-error`} className={onboardingFieldErrorClass}>{taskError}</p> : null}
						</div>
						<div className="flex flex-col gap-2">
							<label htmlFor={RETRIES_FIELD_ID} className={onboardingFormLabelClass}>{t("orchestrator.form.retries")}</label>
							<Select value={retries} onValueChange={setRetries} disabled={busy}>
								<SelectTrigger id={RETRIES_FIELD_ID} size="sm" className="w-40 text-control" aria-label={t("orchestrator.form.retries")}>
									<SelectValue />
								</SelectTrigger>
								<SelectContent position="popper" side="bottom" align="start" sideOffset={4}>
									{retryLabelKeys.slice(0, CLAUDE_ORCHESTRATOR_MAX_RETRIES + 1).map((key, value) => (
										<SelectItem key={key} value={String(value)}>{t(key)}</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>
						<label htmlFor={CONSENT_FIELD_ID} className="flex items-start gap-2.5 text-[13px] leading-5">
							<Checkbox id={CONSENT_FIELD_ID} className="mt-0.5" checked={consent} disabled={busy} onCheckedChange={(checked) => setConsent(checked === true)} />
							<span>{t("orchestrator.form.consent", { provider: providerName(info.workerProvider) })}</span>
						</label>
						{!info.sandboxed ? <p className={onboardingFieldHintClass}>{t("orchestrator.form.unsandboxed")}</p> : null}
						{startError ? <p role="alert" className={onboardingFieldErrorClass}>{t(`orchestrator.error.${startError}`)}</p> : null}
					</div>
					<div className={cn(onboardingFooterActionsEndClass, "px-4 pb-4")}>
						<Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>{t("orchestrator.form.cancel")}</Button>
						<Button type="submit" variant="primary" disabled={busy || !consent || task.trim() === ""}>
							{busy ? <LoaderCircle className="size-3.5 animate-spin motion-reduce:animate-none" aria-hidden="true" /> : null}
							{t(busy ? "orchestrator.form.starting" : "orchestrator.form.start")}
						</Button>
					</div>
				</form>
			</DialogContent>
		</Dialog>
	);
}
