import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
	isTerminalOrchestratorRunState,
	ORCHESTRATOR_RUN_WIRE_STATES,
	toOrchestratorRunViewModel,
	type OrchestratorRunSnapshot,
	type OrchestratorRunViewModel,
	type OrchestratorRunWireState,
} from "@aoagents/product-ui";

/** The experimental internal routes are not part of generated OpenAPI yet. */
export type StartOrchestratorRunRequest = {
	task: string;
	maxRetries?: number;
	explicitOptIn: true;
};
export type OrchestratorRunRequestOptions = { signal: AbortSignal };

/**
 * Inject the host API client rather than pretending these local-only routes
 * are represented in the generated schema. Implementations must not add auth
 * headers or browser credentials; run payloads deliberately have no credential
 * fields. The current daemon does not yet expose the cancel route.
 */
export interface OrchestratorRunClient {
	startRun(request: StartOrchestratorRunRequest, options: OrchestratorRunRequestOptions): Promise<OrchestratorRunSnapshot>;
	getRunStatus(runId: string, options: OrchestratorRunRequestOptions): Promise<OrchestratorRunSnapshot>;
	cancelRun(runId: string, options: OrchestratorRunRequestOptions): Promise<OrchestratorRunSnapshot>;
}

export type OrchestratorRunError = "disabled" | "consent_required" | "invalid_task" | "request_failed";
export type UseOrchestratorRunOptions = {
	client: OrchestratorRunClient;
	/** Defaults off. This must be wired to an explicit host feature flag. */
	enabled?: boolean;
	/** Start requests are denied until the user has explicitly consented. */
	consentGranted?: boolean;
	/** Defaults off because cancellation is not currently exposed by the daemon. */
	cancelEnabled?: boolean;
	pollIntervalMs?: number;
};
export type StartOrchestratorRunInput = { task: string; maxRetries?: number };
export type UseOrchestratorRunResult = {
	run: OrchestratorRunViewModel | null;
	isStarting: boolean;
	isCancelling: boolean;
	error: OrchestratorRunError | null;
	startRun: (input: StartOrchestratorRunInput) => Promise<OrchestratorRunViewModel | null>;
	cancelRun: () => Promise<boolean>;
};

function isWireState(value: unknown): value is OrchestratorRunWireState {
	return typeof value === "string" && ORCHESTRATOR_RUN_WIRE_STATES.some((state) => state === value);
}

/** Rebuild only allowlisted fields so untrusted response extras never enter UI state. */
function normalizeSnapshot(value: unknown): OrchestratorRunSnapshot {
	if (typeof value !== "object" || value === null) throw new Error("Invalid run response");
	const candidate = value as { runId?: unknown; state?: unknown };
	if (typeof candidate.runId !== "string" || candidate.runId.trim() === "" || !isWireState(candidate.state)) {
		throw new Error("Invalid run response");
	}
	return { runId: candidate.runId, state: candidate.state };
}

function validStartInput(input: StartOrchestratorRunInput): boolean {
	if (input.task.trim() === "" || new TextEncoder().encode(input.task).byteLength > 8 * 1024) return false;
	return input.maxRetries === undefined || (Number.isInteger(input.maxRetries) && input.maxRetries >= 0 && input.maxRetries <= 3);
}

function safeError(): OrchestratorRunError {
	// Do not expose arbitrary server/client error text, which could contain secrets.
	return "request_failed";
}

export function useOrchestratorRun({
	client,
	enabled = false,
	consentGranted = false,
	cancelEnabled = false,
	pollIntervalMs = 1_000,
}: UseOrchestratorRunOptions): UseOrchestratorRunResult {
	const [snapshot, setSnapshot] = useState<OrchestratorRunSnapshot | null>(null);
	const [isStarting, setIsStarting] = useState(false);
	const [isCancelling, setIsCancelling] = useState(false);
	const [error, setError] = useState<OrchestratorRunError | null>(null);
	const snapshotRef = useRef(snapshot);
	const enabledRef = useRef(enabled);
	const consentRef = useRef(consentGranted);
	const cancelEnabledRef = useRef(cancelEnabled);
	const startingRef = useRef(false);
	const startControllerRef = useRef<AbortController | null>(null);
	const cancelControllerRef = useRef<AbortController | null>(null);
	snapshotRef.current = snapshot;
	enabledRef.current = enabled;
	consentRef.current = consentGranted;
	cancelEnabledRef.current = cancelEnabled;

	const interval = Number.isFinite(pollIntervalMs) && pollIntervalMs > 0 ? pollIntervalMs : 1_000;
	const activeRunId = snapshot?.runId ?? null;
	const terminal = snapshot ? isTerminalOrchestratorRunState(snapshot.state) : true;

	useEffect(() => {
		if (enabled) return;
		startControllerRef.current?.abort();
		cancelControllerRef.current?.abort();
	}, [enabled]);

	useEffect(() => () => {
		startControllerRef.current?.abort();
		cancelControllerRef.current?.abort();
	}, []);

	useEffect(() => {
		if (!enabled || !activeRunId || terminal) return;
		let disposed = false;
		let timer: ReturnType<typeof setTimeout> | undefined;
		let controller: AbortController | undefined;

		const poll = async () => {
			controller = new AbortController();
			try {
				const response = normalizeSnapshot(await client.getRunStatus(activeRunId, { signal: controller.signal }));
				if (response.runId !== activeRunId) throw new Error("Mismatched run response");
				if (disposed) return;
				setSnapshot(response);
				setError(null);
				if (!isTerminalOrchestratorRunState(response.state)) timer = setTimeout(() => void poll(), interval);
			} catch {
				if (disposed || controller?.signal.aborted) return;
				setError(safeError());
				timer = setTimeout(() => void poll(), interval);
			}
		};

		timer = setTimeout(() => void poll(), interval);
		return () => {
			disposed = true;
			if (timer !== undefined) clearTimeout(timer);
			controller?.abort();
		};
	}, [activeRunId, client, enabled, interval, terminal]);

	const startRun = useCallback(async (input: StartOrchestratorRunInput) => {
		if (!enabledRef.current) {
			setError("disabled");
			return null;
		}
		if (!consentRef.current) {
			setError("consent_required");
			return null;
		}
		if (!validStartInput(input) || startingRef.current) {
			setError("invalid_task");
			return null;
		}

		startingRef.current = true;
		setIsStarting(true);
		setError(null);
		const controller = new AbortController();
		startControllerRef.current = controller;
		try {
			// Do not spread caller input: the wire payload is explicitly allowlisted.
			const request: StartOrchestratorRunRequest = {
				task: input.task,
				explicitOptIn: true,
				...(input.maxRetries === undefined ? {} : { maxRetries: input.maxRetries }),
			};
			const next = normalizeSnapshot(await client.startRun(request, { signal: controller.signal }));
			if (controller.signal.aborted || !enabledRef.current) return null;
			setSnapshot(next);
			return toOrchestratorRunViewModel(next, { cancelAvailable: cancelEnabledRef.current });
		} catch {
			if (!controller.signal.aborted) setError(safeError());
			return null;
		} finally {
			startingRef.current = false;
			startControllerRef.current = null;
			setIsStarting(false);
		}
	}, [client]);

	const cancelRun = useCallback(async (): Promise<boolean> => {
		const current = snapshotRef.current;
		if (!enabledRef.current || !cancelEnabledRef.current || !current || isTerminalOrchestratorRunState(current.state)) return false;
		setIsCancelling(true);
		setError(null);
		const controller = new AbortController();
		cancelControllerRef.current = controller;
		try {
			const next = normalizeSnapshot(await client.cancelRun(current.runId, { signal: controller.signal }));
			if (next.runId !== current.runId) throw new Error("Mismatched run response");
			if (controller.signal.aborted || !enabledRef.current) return false;
			setSnapshot(next);
			return true;
		} catch {
			if (!controller.signal.aborted) setError(safeError());
			return false;
		} finally {
			cancelControllerRef.current = null;
			setIsCancelling(false);
		}
	}, [client]);

	const run = useMemo(
		() => snapshot ? toOrchestratorRunViewModel(snapshot, { cancelAvailable: enabled && cancelEnabled }) : null,
		[cancelEnabled, enabled, snapshot],
	);

	return { run, isStarting, isCancelling, error, startRun, cancelRun };
}
