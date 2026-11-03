// X-00's ears: the microphone, recorded in the renderer and handed to the
// daemon's speech engine as 16 kHz mono 16-bit WAV. Recording stops on its own
// once the person has spoken and then paused, or when they press the button
// again. Nothing is recorded until they press it.

import { X00_MAX_LISTEN_SECONDS, X00_SAMPLE_RATE } from "../../shared/x00";

/** Averages blocks of samples down to `toRate` (a box filter is enough for speech). */
export function downsample(input: Float32Array, fromRate: number, toRate = X00_SAMPLE_RATE): Float32Array {
	if (fromRate === toRate) return input.slice();
	if (fromRate < toRate || fromRate <= 0) throw new Error("x00-ear: cannot upsample");
	const ratio = fromRate / toRate;
	const length = Math.floor(input.length / ratio);
	const output = new Float32Array(length);
	for (let i = 0; i < length; i += 1) {
		const start = Math.floor(i * ratio);
		const end = Math.min(input.length, Math.floor((i + 1) * ratio));
		let sum = 0;
		for (let j = start; j < end; j += 1) sum += input[j];
		output[i] = end > start ? sum / (end - start) : 0;
	}
	return output;
}

/** Encodes samples (-1..1) as a 16-bit PCM mono WAV file. */
export function encodeWav(samples: Float32Array, rate = X00_SAMPLE_RATE): Uint8Array {
	const bytes = new Uint8Array(44 + samples.length * 2);
	const view = new DataView(bytes.buffer);
	const ascii = (offset: number, text: string) => {
		for (let i = 0; i < text.length; i += 1) view.setUint8(offset + i, text.charCodeAt(i));
	};
	ascii(0, "RIFF");
	view.setUint32(4, 36 + samples.length * 2, true);
	ascii(8, "WAVE");
	ascii(12, "fmt ");
	view.setUint32(16, 16, true);
	view.setUint16(20, 1, true);
	view.setUint16(22, 1, true);
	view.setUint32(24, rate, true);
	view.setUint32(28, rate * 2, true);
	view.setUint16(32, 2, true);
	view.setUint16(34, 16, true);
	ascii(36, "data");
	view.setUint32(40, samples.length * 2, true);
	for (let i = 0; i < samples.length; i += 1) {
		const clamped = Math.max(-1, Math.min(1, samples[i]));
		view.setInt16(44 + i * 2, clamped < 0 ? clamped * 0x8000 : clamped * 0x7fff, true);
	}
	return bytes;
}

/** Root-mean-square loudness of a block. */
export function rms(samples: Float32Array): number {
	if (samples.length === 0) return 0;
	let sum = 0;
	for (let i = 0; i < samples.length; i += 1) sum += samples[i] * samples[i];
	return Math.sqrt(sum / samples.length);
}

/** Lifts a quiet recording so the engine hears it well, without clipping. */
export function normalize(samples: Float32Array, target = 0.9): Float32Array {
	let peak = 0;
	for (let i = 0; i < samples.length; i += 1) peak = Math.max(peak, Math.abs(samples[i]));
	if (peak < 0.005 || peak >= target) return samples;
	const gain = Math.min(8, target / peak);
	const output = new Float32Array(samples.length);
	for (let i = 0; i < samples.length; i += 1) output[i] = samples[i] * gain;
	return output;
}

export type SilenceVerdict = "continue" | "stop" | "no_speech";

/**
 * Decides when the person has finished: speech is what rises clearly above
 * the room's own noise; after some speech, a pause ends the recording. With
 * no speech at all for a while, it gives up.
 */
export class SilenceDetector {
	private floor = 0.01;
	private speechMs = 0;
	private lastSpeech = 0;
	private started: number | null = null;

	constructor(private readonly options: { silenceMs?: number; noSpeechMs?: number; minSpeechMs?: number } = {}) {}

	get heardSpeech(): boolean {
		return this.speechMs >= (this.options.minSpeechMs ?? 250);
	}

	push(level: number, now: number, blockMs: number): SilenceVerdict {
		this.started ??= now;
		const speaking = level > Math.max(0.02, this.floor * 3);
		if (!speaking) {
			// The floor follows the room slowly, and only while nobody speaks.
			this.floor = this.floor * 0.95 + level * 0.05;
		}
		if (speaking) {
			this.speechMs += blockMs;
			this.lastSpeech = now;
			return "continue";
		}
		if (!this.heardSpeech) {
			return now - this.started >= (this.options.noSpeechMs ?? 8000) ? "no_speech" : "continue";
		}
		return now - this.lastSpeech >= (this.options.silenceMs ?? 1300) ? "stop" : "continue";
	}
}

export type RecorderError = "denied" | "no_mic" | "unsupported";

export class X00RecorderError extends Error {
	constructor(readonly code: RecorderError) {
		super(`x00-ear: ${code}`);
	}
}

type RecorderOptions = {
	/** Loudness 0..1, about 10 times a second, for the button's ring. */
	onLevel?: (level: number) => void;
	/** The person stopped talking (or never started): call stop(). */
	onAutoStop?: (verdict: Exclude<SilenceVerdict, "continue">) => void;
};

/** One recording at a time from the default microphone. */
export class X00Recorder {
	private stream: MediaStream | null = null;
	private context: AudioContext | null = null;
	private processor: ScriptProcessorNode | null = null;
	private chunks: Float32Array[] = [];
	private detector = new SilenceDetector();
	private rate = 48000;
	private samples = 0;

	static supported(): boolean {
		return typeof navigator !== "undefined" && typeof navigator.mediaDevices?.getUserMedia === "function" && typeof window.AudioContext === "function";
	}

	get recording(): boolean {
		return this.stream !== null;
	}

	async start(options: RecorderOptions = {}): Promise<void> {
		if (this.stream) return;
		if (!X00Recorder.supported()) throw new X00RecorderError("unsupported");
		let stream: MediaStream;
		try {
			stream = await navigator.mediaDevices.getUserMedia({
				audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true, autoGainControl: true },
			});
		} catch (error) {
			const name = error instanceof DOMException ? error.name : "";
			throw new X00RecorderError(name === "NotAllowedError" || name === "SecurityError" ? "denied" : "no_mic");
		}
		this.stream = stream;
		this.chunks = [];
		this.samples = 0;
		this.detector = new SilenceDetector();
		const context = new AudioContext();
		this.context = context;
		this.rate = context.sampleRate;
		const source = context.createMediaStreamSource(stream);
		// ScriptProcessor is old but needs no worklet module, so it works under
		// the app's content security policy.
		const processor = context.createScriptProcessor(4096, 1, 1);
		this.processor = processor;
		const mute = context.createGain();
		mute.gain.value = 0;
		const maxSamples = (X00_MAX_LISTEN_SECONDS - 1) * this.rate;
		let stopped = false;
		processor.onaudioprocess = (event) => {
			if (!this.stream || stopped) return;
			const block = new Float32Array(event.inputBuffer.getChannelData(0));
			this.chunks.push(block);
			this.samples += block.length;
			const level = rms(block);
			options.onLevel?.(Math.min(1, level * 6));
			const verdict = this.samples >= maxSamples ? "stop" : this.detector.push(level, performance.now(), (block.length / this.rate) * 1000);
			if (verdict !== "continue") {
				stopped = true;
				options.onAutoStop?.(verdict);
			}
		};
		source.connect(processor);
		processor.connect(mute);
		mute.connect(context.destination);
		if (context.state === "suspended") await context.resume().catch(() => undefined);
	}

	/** Ends the recording; returns the WAV, or null when nobody spoke. */
	async stop(): Promise<Uint8Array | null> {
		const heard = this.detector.heardSpeech;
		const chunks = this.chunks;
		const rate = this.rate;
		await this.release();
		if (!heard || chunks.length === 0) return null;
		const total = chunks.reduce((sum, chunk) => sum + chunk.length, 0);
		const joined = new Float32Array(total);
		let offset = 0;
		for (const chunk of chunks) {
			joined.set(chunk, offset);
			offset += chunk.length;
		}
		return encodeWav(normalize(downsample(joined, rate)), X00_SAMPLE_RATE);
	}

	/** Drops the recording. */
	cancel(): void {
		void this.release();
	}

	private async release(): Promise<void> {
		this.processor?.disconnect();
		if (this.processor) this.processor.onaudioprocess = null;
		this.processor = null;
		for (const track of this.stream?.getTracks() ?? []) track.stop();
		this.stream = null;
		this.chunks = [];
		const context = this.context;
		this.context = null;
		await context?.close().catch(() => undefined);
	}
}
