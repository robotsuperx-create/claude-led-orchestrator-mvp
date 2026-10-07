import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
	isTerminalClaudeOrchestratorState,
	type ClaudeOrchestratorErrorCode,
	type ClaudeOrchestratorInfo,
	type ClaudeOrchestratorResult,
	type ClaudeOrchestratorRun,
	type ClaudeOrchestratorStartInput,
} from "../../shared/claude-orchestrator";
import { aoBridge } from "../lib/bridge";
import type { ClaudeOrchestratorBridge } from "../lib/claude-orchestrator-preview";

export const claudeOrchestratorQueryKeys = {
	info: ["claude-orchestrator", "info"] as const,
	runs: ["claude-orchestrator", "runs"] as const,
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
		mutationFn: (input) => unwrap((bridge) => bridge.start({ task: input.task, maxRetries: input.maxRetries })),
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
