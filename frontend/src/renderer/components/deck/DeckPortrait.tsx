import { useEffect, useRef } from "react";
import x00Still from "../../assets/x00/x00.webp";
import { DECK_LIGHT_COLOUR, type DeckLight } from "../../lib/x-robot-deck";
import { cn } from "../../lib/utils";
import { Portrait } from "../../lib/x-robot/portrait.js";

const LIGHTS: readonly DeckLight[] = ["watching", "deciding", "long", "short", "veto"];

/**
 * X-00 exactly as the trading deck draws him: the deck's own portrait code
 * (lib/x-robot/portrait.js) in the deck's `.portrait` frame with its halo.
 * The light follows `light`; his mouth follows `voice` while he speaks.
 */
export function DeckPortrait({
	light,
	speaking,
	voice,
	className,
}: {
	light: DeckLight;
	speaking: boolean;
	voice?: (now: number) => number;
	className?: string;
}) {
	const host = useRef<HTMLDivElement>(null);
	const portrait = useRef<Portrait | null>(null);
	const voiceRef = useRef(voice);
	voiceRef.current = voice;

	useEffect(() => {
		const element = host.current;
		if (!element) return;
		let instance: Portrait | null = null;
		try {
			instance = new Portrait(element, { art: "x00h", state: "watching", still: x00Still });
			instance.setVoice((now) => voiceRef.current?.(now) ?? 0);
		} catch {
			// No WebGL and no still: the halo alone keeps his place.
			instance = null;
		}
		portrait.current = instance;
		return () => {
			instance?.dispose();
			portrait.current = null;
		};
	}, []);

	// The portrait owns its host's state classes (as on the deck), so React
	// leaves the class list alone after the first render.
	useEffect(() => {
		const element = host.current;
		if (portrait.current) portrait.current.setState(light);
		else if (element) {
			for (const state of LIGHTS) element.classList.toggle(state, state === light);
			element.style.setProperty("--light", DECK_LIGHT_COLOUR[light]);
		}
	}, [light]);

	useEffect(() => {
		portrait.current?.setSpeaking(speaking);
		host.current?.classList.toggle("speaking", speaking);
	}, [speaking]);

	return (
		<div ref={host} className={cn("portrait", className)} data-testid="deck-portrait" data-light={light}>
			<div className="halo" />
		</div>
	);
}
