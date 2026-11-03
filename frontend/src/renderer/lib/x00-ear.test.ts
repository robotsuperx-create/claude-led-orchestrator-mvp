import { describe, expect, it } from "vitest";
import { downsample, encodeWav, normalize, rms, SilenceDetector } from "./x00-ear";

describe("x00 ear", () => {
	it("encodes 16 kHz mono 16-bit WAV the daemon accepts", () => {
		const wav = encodeWav(new Float32Array([0, 1, -1, 0.5, 2]), 16000);
		const view = new DataView(wav.buffer);
		const text = (offset: number, size: number) => String.fromCharCode(...wav.slice(offset, offset + size));
		expect(text(0, 4)).toBe("RIFF");
		expect(text(8, 8)).toBe("WAVEfmt ");
		expect(view.getUint16(20, true)).toBe(1);
		expect(view.getUint16(22, true)).toBe(1);
		expect(view.getUint32(24, true)).toBe(16000);
		expect(view.getUint16(34, true)).toBe(16);
		expect(text(36, 4)).toBe("data");
		expect(view.getUint32(40, true)).toBe(10);
		expect(wav.byteLength).toBe(54);
		expect([1, 2, 3, 4, 5].map((i) => view.getInt16(42 + i * 2, true))).toEqual([0, 32767, -32768, 16383, 32767]);
	});

	it("downsamples a 48 kHz recording to 16 kHz by averaging", () => {
		const input = new Float32Array(48000).map((_, i) => (i % 3 === 0 ? 0.3 : 0));
		const output = downsample(input, 48000);
		expect(output.length).toBe(16000);
		expect(output[0]).toBeCloseTo(0.1);
		expect(downsample(new Float32Array([1, 2]), 16000)).toEqual(new Float32Array([1, 2]));
		expect(() => downsample(new Float32Array(4), 8000)).toThrow();
	});

	it("lifts quiet speech without clipping and leaves silence alone", () => {
		const quiet = normalize(new Float32Array([0.1, -0.05]));
		expect(Math.max(...quiet.map(Math.abs))).toBeCloseTo(0.8);
		const silence = new Float32Array([0.001, -0.002]);
		expect(normalize(silence)).toBe(silence);
		expect(rms(new Float32Array([0.5, -0.5]))).toBeCloseTo(0.5);
	});

	it("stops after a pause that follows speech", () => {
		const detector = new SilenceDetector({ silenceMs: 1000, minSpeechMs: 200 });
		let now = 0;
		const step = (level: number) => detector.push(level, (now += 100), 100);
		for (let i = 0; i < 5; i += 1) expect(step(0.005)).toBe("continue");
		for (let i = 0; i < 5; i += 1) expect(step(0.2)).toBe("continue");
		expect(detector.heardSpeech).toBe(true);
		for (let i = 0; i < 9; i += 1) expect(step(0.006)).toBe("continue");
		expect(step(0.006)).toBe("stop");
	});

	it("gives up when nobody speaks", () => {
		const detector = new SilenceDetector({ noSpeechMs: 1000 });
		let verdict = "continue";
		for (let now = 0; now <= 1000 && verdict === "continue"; now += 100) verdict = detector.push(0.004, now, 100);
		expect(verdict).toBe("no_speech");
		expect(detector.heardSpeech).toBe(false);
	});

	it("does not mistake a noisy room for speech once it has learned the noise", () => {
		const detector = new SilenceDetector({ noSpeechMs: 60_000 });
		let now = 0;
		for (let i = 0; i < 100; i += 1) detector.push(0.015, (now += 100), 100);
		expect(detector.heardSpeech).toBe(false);
		for (let i = 0; i < 4; i += 1) detector.push(0.15, (now += 100), 100);
		expect(detector.heardSpeech).toBe(true);
	});
});
