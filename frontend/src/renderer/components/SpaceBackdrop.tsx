import { useEffect, useRef } from "react";
import { cn } from "../lib/utils";
import { SpaceBackdropEngine } from "../lib/space-backdrop";

/**
 * Live deep space: black with bright blue stars. Purely decorative; it never
 * takes pointer events. The x-robot dark palette shows it; other palettes and
 * light mode hide it (see .xr-space in styles.css).
 */
export function SpaceBackdrop({ className }: { className?: string }) {
	const canvas = useRef<HTMLCanvasElement>(null);
	useEffect(() => {
		const element = canvas.current;
		if (!element) return;
		const engine = new SpaceBackdropEngine(element);
		return () => engine.dispose();
	}, []);
	return <canvas ref={canvas} aria-hidden="true" className={cn("xr-space", className)} data-testid="space-backdrop" />;
}
