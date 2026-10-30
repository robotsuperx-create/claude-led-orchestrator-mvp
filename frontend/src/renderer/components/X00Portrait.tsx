import { useEffect, useRef, useState } from "react";
import x00Data from "../assets/x00/x00-data.webp";
import x00Mark from "../assets/x00/x00-mark.webp";
import x00Still from "../assets/x00/x00.webp";
import { cn } from "../lib/utils";
import { type X00State, X00PortraitEngine } from "../lib/x00-portrait";

export type { X00State } from "../lib/x00-portrait";

/**
 * X-00, the commander, as a living relief: he watches while the fleet is idle,
 * breathes while the crew works, takes the outcome's light when a run lands,
 * and goes dark when one fails. The still image stands in when WebGL is
 * missing, lost, or too slow. Decorative unless given a label.
 */
export function X00Portrait({ state, className, label }: { state: X00State; className?: string; label?: string }) {
	const host = useRef<HTMLDivElement>(null);
	const engine = useRef<X00PortraitEngine | null>(null);
	const [still, setStill] = useState(false);
	const initialState = useRef(state);

	useEffect(() => {
		const element = host.current;
		if (!element) return;
		const instance = new X00PortraitEngine(element, {
			art: { map: x00Still, data: x00Data },
			state: initialState.current,
			onFallback: () => setStill(true),
		});
		engine.current = instance;
		return () => {
			instance.dispose();
			engine.current = null;
		};
	}, []);

	useEffect(() => {
		engine.current?.setState(state);
	}, [state]);

	return (
		<div
			ref={host}
			className={cn("x00", className)}
			data-state={state}
			data-still={still ? "true" : undefined}
			data-testid="x00-portrait"
			role={label ? "img" : undefined}
			aria-label={label}
			aria-hidden={label ? undefined : true}
		>
			<div className="x00-halo" aria-hidden="true" />
			{still ? <img className="x00-still" src={x00Still} alt="" draggable={false} /> : null}
		</div>
	);
}

/** X-00's head on a void disc: the product mark at small sizes. Decorative by default. */
export function X00Mark({ className }: { className?: string }) {
	return <img src={x00Mark} alt="" aria-hidden="true" draggable={false} className={cn("xr-mark", className)} />;
}
