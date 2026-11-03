// X-00's voice: the system's own speech synthesis (Windows, macOS and Linux
// voices installed on this computer), so nothing leaves the machine to be
// spoken. While he talks, an envelope built from word boundaries and a
// syllable rhythm moves his jaw.

export type X00Language = "ar" | "en" | "other";

const ARABIC = /[؀-ۿݐ-ݿࢠ-ࣿﭐ-﷿ﹰ-﻿]/;

/** The language a line is spoken in: Arabic script means Arabic. */
export function speechLanguage(text: string, appLocale: string): X00Language {
	if (ARABIC.test(text)) return "ar";
	if (/[A-Za-z]/.test(text)) return appLocale.startsWith("ar") ? "en" : appLocale.startsWith("en") ? "en" : "other";
	return appLocale.startsWith("ar") ? "ar" : "en";
}

// Deeper, calmer voices first; any voice of the right language comes next.
const PREFERRED: Record<"ar" | "en", RegExp[]> = {
	ar: [/hamed/i, /naayf/i, /shakir/i, /hoda/i, /salma/i, /zariyah/i],
	en: [/guy/i, /christopher/i, /ryan/i, /david/i, /mark/i, /george/i, /daniel/i, /zira/i],
};

type VoiceLike = Pick<SpeechSynthesisVoice, "name" | "lang" | "voiceURI" | "localService" | "default">;

/** Chooses the voice for a line: the person's pick when it fits, then the best local voice. */
export function pickVoice<V extends VoiceLike>(voices: readonly V[], language: X00Language, appLocale: string, preferredURI?: string | null): V | null {
	const lang = language === "other" ? appLocale.split("-")[0].toLowerCase() : language;
	const matching = voices.filter((voice) => voice.lang.toLowerCase().startsWith(lang));
	if (preferredURI) {
		const chosen = matching.find((voice) => voice.voiceURI === preferredURI);
		if (chosen) return chosen;
	}
	if (lang === "ar" || lang === "en") {
		for (const pattern of PREFERRED[lang]) {
			const found = matching.find((voice) => pattern.test(voice.name));
			if (found) return found;
		}
	}
	return matching.find((voice) => voice.localService) ?? matching[0] ?? null;
}

/** Speech can be read aloud here. */
export function canSpeak(): boolean {
	return typeof window !== "undefined" && "speechSynthesis" in window && typeof window.SpeechSynthesisUtterance === "function";
}

/** Voices load asynchronously in Chromium; resolve once they are there (or after a short wait). */
export function loadVoices(): Promise<SpeechSynthesisVoice[]> {
	if (!canSpeak()) return Promise.resolve([]);
	const synth = window.speechSynthesis;
	const now = synth.getVoices();
	if (now.length > 0) return Promise.resolve(now);
	return new Promise((resolve) => {
		const done = () => {
			synth.removeEventListener("voiceschanged", done);
			resolve(synth.getVoices());
		};
		synth.addEventListener("voiceschanged", done);
		setTimeout(done, 1500);
	});
}

/** A jaw envelope: word boundaries open the mouth, a syllable rhythm keeps it moving. */
export class SpeechEnvelope {
	private pulse = 0;
	private lastPulse = 0;
	private active = false;
	private started = 0;

	start(now = performance.now()): void {
		this.active = true;
		this.started = now;
		this.pulse = 1;
		this.lastPulse = now;
	}

	boundary(now = performance.now()): void {
		this.pulse = 1;
		this.lastPulse = now;
	}

	stop(): void {
		this.active = false;
	}

	readonly level = (now: number): number => {
		if (!this.active) return 0;
		const sincePulse = (now - this.lastPulse) / 1000;
		const decay = Math.max(0, this.pulse - sincePulse * 2.6);
		const t = (now - this.started) / 1000;
		const syllables = 0.35 + 0.3 * Math.abs(Math.sin(t * 11.3)) * (0.6 + 0.4 * Math.sin(t * 2.7));
		return Math.min(1, 0.25 + 0.55 * decay + 0.35 * syllables);
	};
}

export type SpeakOptions = {
	appLocale: string;
	voiceURI?: string | null;
	envelope?: SpeechEnvelope;
	onStart?: () => void;
	onEnd?: () => void;
};

/**
 * Speaks one line and returns a function that stops it. Only one line plays
 * at a time: a new line cuts off the last.
 */
export async function speak(text: string, options: SpeakOptions): Promise<() => void> {
	const line = text.trim();
	if (!line || !canSpeak()) {
		options.onEnd?.();
		return () => undefined;
	}
	const synth = window.speechSynthesis;
	synth.cancel();
	const voices = await loadVoices();
	const language = speechLanguage(line, options.appLocale);
	const voice = pickVoice(voices, language, options.appLocale, options.voiceURI);
	const utterance = new SpeechSynthesisUtterance(line);
	if (voice) {
		utterance.voice = voice;
		utterance.lang = voice.lang;
	} else {
		utterance.lang = language === "ar" ? "ar" : language === "en" ? "en-US" : options.appLocale;
	}
	// A touch lower and steadier than the default: a commander, not an announcer.
	utterance.pitch = 0.82;
	utterance.rate = language === "ar" ? 0.98 : 1.02;
	let finished = false;
	const finish = () => {
		if (finished) return;
		finished = true;
		options.envelope?.stop();
		options.onEnd?.();
	};
	utterance.onstart = () => {
		options.envelope?.start();
		options.onStart?.();
	};
	utterance.onboundary = () => options.envelope?.boundary();
	utterance.onend = finish;
	utterance.onerror = finish;
	synth.speak(utterance);
	return () => {
		if (!finished) synth.cancel();
		finish();
	};
}
