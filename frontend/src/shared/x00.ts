// Shared contract for X-00, the commander robot. The renderer talks to him
// through the main process, which calls the daemon's loopback-only
// /internal/x00 routes; the Claude key never leaves the daemon. X-00 only
// proposes actions: the app shows each one and runs it when the person
// confirms.

import { isClaudeOrchestratorRunId } from "./claude-orchestrator";

export type X00Role = "user" | "assistant";
export type X00Turn = { role: X00Role; content: string };

export type X00Screen = "mission_control" | "settings" | "home";
export const X00_SCREENS: readonly X00Screen[] = ["mission_control", "settings", "home"];

export type X00Mood = "neutral" | "pleased" | "concerned";

export type X00Action =
	| { type: "start_run"; task: string; approvePlan: boolean }
	| { type: "approve_plan"; runId: string }
	| { type: "reject_plan"; runId: string }
	| { type: "cancel_run"; runId: string }
	| { type: "publish_run"; runId: string }
	| { type: "open"; screen: X00Screen };

/** voice: the line written for a speech engine (Arabic with full tashkeel). */
export type X00Reply = { say: string; voice?: string; mood: X00Mood; actions: X00Action[] };

/** Why X-00 is silent: no Claude key, turned off, or a setting needs fixing. */
export type X00OffReason = "no_key" | "off" | "invalid_url" | "invalid_model";

/** Which engine hears or speaks for X-00: a cloud speech service or one on this computer. */
export type X00VoiceEngine = "local" | "cloud";

export type X00VoiceStatus = { listen?: X00VoiceEngine; speak?: X00VoiceEngine };

export type X00Info = ({ enabled: true; model: string } | { enabled: false; reason: X00OffReason }) & X00VoiceStatus;

export type X00ChatInput = { turns: X00Turn[]; locale: string; screen?: X00Screen | "other" };

/** Why a call failed. Never carries daemon or provider text. */
export type X00ErrorCode = "daemon_unavailable" | "disabled" | "busy" | "invalid" | "timeout" | "provider_failed" | "failed";

export type X00Result<T> = { ok: true; value: T } | { ok: false; error: X00ErrorCode };

/** A recording to transcribe: 16 kHz mono 16-bit WAV. */
export type X00ListenInput = { audio: Uint8Array; locale: string };
export type X00Heard = { text: string };
/** A line to voice. */
export type X00SpeakInput = { text: string; locale: string };
export type X00SpeechType = "audio/wav" | "audio/mpeg";
export type X00Speech = { audio: Uint8Array; type: X00SpeechType };

// Mirrors backend/internal/service/x00voice.
export const X00_SAMPLE_RATE = 16000;
export const X00_MAX_LISTEN_SECONDS = 60;
export const X00_MAX_AUDIO_BYTES = 44 + X00_SAMPLE_RATE * 2 * X00_MAX_LISTEN_SECONDS + 1024;
export const X00_MAX_SPEAK_CHARS = 3000;
export const X00_MAX_HEARD_CHARS = 4000;
export const X00_MAX_SPEECH_BYTES = 16 * 1024 * 1024;

// Mirrors the daemon's limits (backend/internal/service/x00).
export const X00_MAX_TURNS = 24;
export const X00_MAX_TURN_CHARS = 4000;
export const X00_MAX_TOTAL_CHARS = 24000;
const X00_MAX_SAY_CHARS = 1600;
const X00_MAX_VOICE_CHARS = 3000;
const X00_MAX_TASK_CHARS = 8000;
const X00_MAX_ACTIONS = 3;
const MODEL_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$/;
const LOCALE_PATTERN = /^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})?$/;

function record(value: unknown): Record<string, unknown> | null {
	return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : null;
}

function length(text: string): number {
	return Array.from(text).length;
}

/** Validates renderer input before it is sent to the daemon. */
export function validX00ChatInput(input: unknown): input is X00ChatInput {
	const candidate = record(input);
	if (!candidate || !Array.isArray(candidate.turns)) return false;
	const turns = candidate.turns as unknown[];
	if (turns.length === 0 || turns.length > X00_MAX_TURNS) return false;
	let total = 0;
	for (const [index, value] of turns.entries()) {
		const turn = record(value);
		if (!turn || (turn.role !== "user" && turn.role !== "assistant") || typeof turn.content !== "string") return false;
		const text = turn.content.trim();
		const size = length(text);
		if (!text || size > X00_MAX_TURN_CHARS) return false;
		total += size;
		if (index === turns.length - 1 && turn.role !== "user") return false;
	}
	if (total > X00_MAX_TOTAL_CHARS) return false;
	if (typeof candidate.locale !== "string" || !LOCALE_PATTERN.test(candidate.locale)) return false;
	if (candidate.screen !== undefined && candidate.screen !== "other" && !X00_SCREENS.includes(candidate.screen as X00Screen)) return false;
	return true;
}

/** The two languages X-00's engines speak well; "" lets the engine decide. */
export function x00SpeechLang(locale: string): "ar" | "en" | "" {
	const lower = locale.toLowerCase();
	return lower.startsWith("ar") ? "ar" : lower.startsWith("en") ? "en" : "";
}

/** A recording the daemon will accept: a WAV file within the size limit. */
export function validX00ListenInput(input: unknown): input is X00ListenInput {
	const candidate = record(input);
	if (!candidate || !(candidate.audio instanceof Uint8Array)) return false;
	const audio = candidate.audio;
	if (audio.byteLength <= 44 || audio.byteLength > X00_MAX_AUDIO_BYTES) return false;
	const riff = String.fromCharCode(audio[0], audio[1], audio[2], audio[3]);
	const wave = String.fromCharCode(audio[8], audio[9], audio[10], audio[11]);
	return riff === "RIFF" && wave === "WAVE" && typeof candidate.locale === "string" && LOCALE_PATTERN.test(candidate.locale);
}

/** A line the daemon will voice. */
export function validX00SpeakInput(input: unknown): input is X00SpeakInput {
	const candidate = record(input);
	if (!candidate || typeof candidate.text !== "string" || typeof candidate.locale !== "string") return false;
	const text = candidate.text.trim();
	return Boolean(text) && length(text) <= X00_MAX_SPEAK_CHARS && !text.includes("\u0000") && LOCALE_PATTERN.test(candidate.locale);
}

function voiceEngine(value: unknown): X00VoiceEngine | undefined {
	return value === "local" || value === "cloud" ? value : undefined;
}

/** Rebuilds the daemon's info from allowlisted fields. */
export function parseX00Info(value: unknown): X00Info | null {
	const body = record(value);
	if (!body || typeof body.enabled !== "boolean") return null;
	const voice: X00VoiceStatus = {};
	const listen = voiceEngine(body.listen);
	const speak = voiceEngine(body.speak);
	if (listen) voice.listen = listen;
	if (speak) voice.speak = speak;
	if (body.enabled) {
		return typeof body.model === "string" && MODEL_PATTERN.test(body.model) ? { enabled: true, model: body.model, ...voice } : null;
	}
	const reason = body.reason;
	return { enabled: false, reason: reason === "off" || reason === "invalid_url" || reason === "invalid_model" ? reason : "no_key", ...voice };
}

/** Rebuilds X-00's reply, keeping only well-formed actions. */
export function parseX00Reply(value: unknown): X00Reply | null {
	const body = record(value);
	if (!body || typeof body.say !== "string" || !body.say.trim()) return null;
	const say = Array.from(body.say.trim()).slice(0, X00_MAX_SAY_CHARS).join("");
	const mood: X00Mood = body.mood === "pleased" || body.mood === "concerned" ? body.mood : "neutral";
	const actions: X00Action[] = [];
	for (const item of Array.isArray(body.actions) ? body.actions : []) {
		if (actions.length === X00_MAX_ACTIONS) break;
		const action = parseX00Action(item);
		if (action) actions.push(action);
	}
	const voice = typeof body.voice === "string" ? Array.from(body.voice.trim()).slice(0, X00_MAX_VOICE_CHARS).join("") : "";
	return voice ? { say, voice, mood, actions } : { say, mood, actions };
}

/** Rebuilds a transcript from the daemon. */
export function parseX00Heard(value: unknown): X00Heard | null {
	const body = record(value);
	if (!body || typeof body.text !== "string") return null;
	return { text: Array.from(body.text.trim()).slice(0, X00_MAX_HEARD_CHARS).join("") };
}

function parseX00Action(value: unknown): X00Action | null {
	const action = record(value);
	if (!action) return null;
	switch (action.type) {
		case "start_run": {
			if (typeof action.task !== "string") return null;
			const task = action.task.trim();
			if (!task || length(task) > X00_MAX_TASK_CHARS) return null;
			return { type: "start_run", task, approvePlan: action.approvePlan === true };
		}
		case "approve_plan":
		case "reject_plan":
		case "cancel_run":
		case "publish_run":
			return isClaudeOrchestratorRunId(action.runId) ? { type: action.type, runId: action.runId } : null;
		case "open":
			return X00_SCREENS.includes(action.screen as X00Screen) ? { type: "open", screen: action.screen as X00Screen } : null;
		default:
			return null;
	}
}
