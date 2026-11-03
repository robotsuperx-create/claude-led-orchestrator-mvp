import { describe, expect, it } from "vitest";
import { pickVoice, SpeechEnvelope, speechChunks, speechLanguage } from "./x00-voice";

const voice = (name: string, lang: string, localService = true) => ({ name, lang, voiceURI: name, localService, default: false });

describe("speechLanguage", () => {
	it("speaks Arabic script in Arabic and Latin text in English", () => {
		expect(speechLanguage("أهلاً أيها القائد", "en")).toBe("ar");
		expect(speechLanguage("Welcome, commander", "ar")).toBe("en");
		expect(speechLanguage("1234", "ar")).toBe("ar");
	});
});

describe("pickVoice", () => {
	const voices = [
		voice("Microsoft Zira - English (United States)", "en-US"),
		voice("Microsoft David - English (United States)", "en-US"),
		voice("Microsoft Hoda - Arabic (Egypt)", "ar-EG"),
		voice("Microsoft Hortense - French (France)", "fr-FR"),
	];

	it("prefers a deeper voice of the line's language", () => {
		expect(pickVoice(voices, "en", "en")?.name).toContain("David");
		expect(pickVoice(voices, "ar", "ar")?.name).toContain("Hoda");
	});

	it("honours the person's pick when it speaks that language", () => {
		expect(pickVoice(voices, "en", "en", "Microsoft Zira - English (United States)")?.name).toContain("Zira");
		expect(pickVoice(voices, "ar", "ar", "Microsoft Zira - English (United States)")?.name).toContain("Hoda");
	});

	it("falls back to the app language and then to nothing", () => {
		expect(pickVoice(voices, "other", "fr")?.name).toContain("Hortense");
		expect(pickVoice([voice("Microsoft Zira", "en-US")], "ar", "ar")).toBeNull();
	});
});

describe("SpeechEnvelope", () => {
	it("is silent until speech starts and after it stops", () => {
		const envelope = new SpeechEnvelope();
		expect(envelope.level(1000)).toBe(0);
		envelope.start(1000);
		const open = envelope.level(1010);
		expect(open).toBeGreaterThan(0.5);
		expect(open).toBeLessThanOrEqual(1);
		envelope.boundary(2000);
		expect(envelope.level(2005)).toBeGreaterThan(envelope.level(2400));
		envelope.stop();
		expect(envelope.level(2500)).toBe(0);
	});
});

describe("speechChunks", () => {
	it("splits a reply into sentences and joins the short ones", () => {
		expect(speechChunks("All calm. One run is held for review and needs your eyes. Another is executing right now, three of five steps done.")).toEqual([
			"All calm. One run is held for review and needs your eyes.",
			"Another is executing right now, three of five steps done.",
		]);
		expect(speechChunks("الأُسْطُولُ هَادِئٌ. هُنَاكَ تَشْغِيلٌ وَاحِدٌ يَنْتَظِرُ مُوَافَقَتَكَ عَلَى الخُطَّةِ؟ نَعَمْ!")).toEqual([
			"الأُسْطُولُ هَادِئٌ. هُنَاكَ تَشْغِيلٌ وَاحِدٌ يَنْتَظِرُ مُوَافَقَتَكَ عَلَى الخُطَّةِ؟ نَعَمْ!",
		]);
		expect(speechChunks("   ")).toEqual([]);
		expect(speechChunks("No punctuation at all")).toEqual(["No punctuation at all"]);
	});
});
