import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
	isTerminalClaudeOrchestratorState,
	type ClaudeOrchestratorErrorCode,
	type ClaudeOrchestratorInfo,
	type ClaudeOrchestratorLive,
	type ClaudeOrchestratorPlan,
	type ClaudeOrchestratorResult,
	type ClaudeOrchestratorRun,
	type ClaudeOrchestratorSitePages,
	type ClaudeOrchestratorStartInput,
	type ClaudeOrchestratorVisual,
} from "../../shared/claude-orchestrator";
import { aoBridge } from "../lib/bridge";
import type { ClaudeOrchestratorBridge } from "../lib/claude-orchestrator-preview";

export const claudeOrchestratorQueryKeys = {
	info: ["claude-orchestrator", "info"] as const,
	runs: ["claude-orchestrator", "runs"] as const,
	plan: (runId: string) => ["claude-orchestrator", "plan", runId] as const,
	visual: (runId: string) => ["claude-orchestrator", "visual", runId] as const,
	visualShot: (runId: string, name: string) => ["claude-orchestrator", "visual", runId, name] as const,
	live: (runId: string) => ["claude-orchestrator", "live", runId] as const,
	sitePages: (runId: string) => ["claude-orchestrator", "site", runId] as const,
};

/** A failed bridge call; `code` is the only detail the renderer ever sees. */
export class ClaudeOrchestratorError extends Error {
	constructor(readonly code: ClaudeOrchestratorErrorCode) {
		super(code);
		this.name = "ClaudeOrchestratorError";
	}
}

/** The Electron bridge, or the preview/unavailable stand-in outside Electron. */
export function resolveClaudeOrchestratorBridge(): ClaudeOrchestratorBridge {
	return aoBridge.claudeOrchestrator;
}

async function unwrap<T>(call: (bridge: ClaudeOrchestratorBridge) => Promise<ClaudeOrchestratorResult<T>>): Promise<T> {
	const result = await call(resolveClaudeOrchestratorBridge());
	if (!result.ok) throw new ClaudeOrchestratorError(result.error);
	return result.value;
}

export function claudeOrchestratorErrorCode(error: unknown): ClaudeOrchestratorErrorCode | null {
	if (!error) return null;
	return error instanceof ClaudeOrchestratorError ? error.code : "failed";
}

export function useClaudeOrchestratorInfo() {
	return useQuery<ClaudeOrchestratorInfo>({
		queryKey: claudeOrchestratorQueryKeys.info,
		queryFn: () => unwrap((bridge) => bridge.info()),
		refetchInterval: 30_000,
		retry: false,
	});
}

/** Polls quickly only while a run is active, so the stage display stays live. */
export function useClaudeOrchestratorRuns(enabled: boolean) {
	return useQuery<ClaudeOrchestratorRun[]>({
		queryKey: claudeOrchestratorQueryKeys.runs,
		queryFn: () => unwrap((bridge) => bridge.list()),
		enabled,
		refetchInterval: (query) => (query.state.data ?? []).some((run) => !isTerminalClaudeOrchestratorState(run.state)) ? 1_500 : 15_000,
		retry: false,
	});
}

export function useStartClaudeOrchestratorRun() {
	const client = useQueryClient();
	return useMutation<ClaudeOrchestratorRun, ClaudeOrchestratorError, ClaudeOrchestratorStartInput>({
		mutationFn: (input) => unwrap((bridge) => bridge.start({ task: input.task, maxRetries: input.maxRetries, ...(input.approvePlan ? { approvePlan: true } : {}) })),
		onSuccess: (run) => {
			// Show the new run immediately; the next poll fills in its title.
			client.setQueryData<ClaudeOrchestratorRun[]>(claudeOrchestratorQueryKeys.runs, (current = []) =>
				current.some((item) => item.runId === run.runId) ? current : [run, ...current]);
			void client.invalidateQueries({ queryKey: claudeOrchestratorQueryKeys.runs });
		},
	});
}

export function useCancelClaudeOrchestratorRun() {
	const client = useQueryClient();
	return useMutation<ClaudeOrchestratorRun, ClaudeOrchestratorError, string>({
		mutationFn: (runId) => unwrap((bridge) => bridge.cancel(runId)),
		onSettled: () => void client.invalidateQueries({ queryKey: claudeOrchestratorQueryKeys.runs }),
	});
}

/** Claude's plan for a run; fetched once the run is waiting for approval. */
export function useClaudeOrchestratorPlan(runId: string, enabled: boolean) {
	return useQuery<ClaudeOrchestratorPlan>({
		queryKey: claudeOrchestratorQueryKeys.plan(runId),
		queryFn: () => unwrap((bridge) => bridge.plan(runId)),
		enabled,
		retry: false,
		// A revised plan for the same run replaces the first one, so never
		// treat a cached plan as current once the review screen remounts.
		staleTime: 0,
	});
}

/** Approves or rejects a run's plan. */
export function useDecideClaudeOrchestratorPlan() {
	const client = useQueryClient();
	return useMutation<ClaudeOrchestratorRun, ClaudeOrchestratorError, { runId: string; approve: boolean }>({
		mutationFn: ({ runId, approve }) => unwrap((bridge) => (approve ? bridge.approve(runId) : bridge.reject(runId))),
		onSettled: () => void client.invalidateQueries({ queryKey: claudeOrchestratorQueryKeys.runs }),
	});
}

/** Pushes a finished run's branch and opens a pull request. Never merges. */
export function usePublishClaudeOrchestratorRun() {
	const client = useQueryClient();
	return useMutation<ClaudeOrchestratorRun, ClaudeOrchestratorError, string>({
		mutationFn: (runId) => unwrap((bridge) => bridge.publish(runId)),
		onSuccess: (run) => {
			client.setQueryData<ClaudeOrchestratorRun[]>(claudeOrchestratorQueryKeys.runs, (current = []) =>
				current.map((item) => (item.runId === run.runId ? { ...item, ...run } : item)));
		},
		onSettled: () => void client.invalidateQueries({ queryKey: claudeOrchestratorQueryKeys.runs }),
	});
}

/** A run's visual review; polled while the run is still working. */
export function useClaudeOrchestratorVisual(runId: string, live: boolean) {
	return useQuery<ClaudeOrchestratorVisual>({
		queryKey: claudeOrchestratorQueryKeys.visual(runId),
		queryFn: () => unwrap((bridge) => bridge.visual(runId)),
		refetchInterval: live ? 3_000 : false,
		retry: false,
	});
}

/** One kept screenshot as a data URL. A kept picture never changes. */
export function useClaudeOrchestratorVisualShot(runId: string, name: string | undefined) {
	return useQuery<string>({
		queryKey: claudeOrchestratorQueryKeys.visualShot(runId, name ?? ""),
		queryFn: () => unwrap((bridge) => bridge.visualShot(runId, name as string)),
		enabled: Boolean(name),
		staleTime: Number.POSITIVE_INFINITY,
		gcTime: 5 * 60_000,
		retry: false,
	});
}

/** What a run is doing right now; polled quickly while it is live, so the deck's code stream and crew map move. */
export function useClaudeOrchestratorLive(runId: string, live: boolean) {
	return useQuery<ClaudeOrchestratorLive>({
		queryKey: claudeOrchestratorQueryKeys.live(runId),
		queryFn: () => unwrap((bridge) => bridge.live(runId)),
		enabled: runId !== "",
		refetchInterval: live ? 1_500 : false,
		retry: false,
	});
}

/** A run's page names for the live preview; none until the run has a worktree with pages. */
export function useClaudeOrchestratorSitePages(runId: string, live: boolean) {
	return useQuery<ClaudeOrchestratorSitePages>({
		queryKey: claudeOrchestratorQueryKeys.sitePages(runId),
		queryFn: () => unwrap((bridge) => bridge.sitePages(runId)),
		enabled: runId !== "",
		refetchInterval: live ? 5_000 : false,
		retry: false,
	});
}
