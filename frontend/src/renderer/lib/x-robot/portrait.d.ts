/** X-00's light states on the deck: the room's light while he watches, decides, or after a verdict. */
export type PortraitState = "watching" | "deciding" | "long" | "short" | "veto";

export declare const REDUCED: boolean;

/** X-00 as drawn on the X-ROBOT trading deck (vendored portrait.js). */
export declare class Portrait {
	constructor(host: HTMLElement, options?: { art?: "x00h"; state?: PortraitState; still?: string | null });
	readonly ok: boolean;
	/** The envelope of what he is saying, 0..1. */
	setVoice(source: (now: number) => number): void;
	setSpeaking(on: boolean): void;
	setState(state: PortraitState): void;
	dispose(): void;
}
