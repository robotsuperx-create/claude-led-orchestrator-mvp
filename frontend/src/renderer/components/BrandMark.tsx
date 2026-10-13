import xRobotMark from "../../../assets/x-robot-mark.svg";
import { BRAND_NAME } from "../../shared/brand";
import { cn } from "../lib/utils";

/** The X-ROBOT-AI mark: a robot crossed by two orbits. Decorative by default. */
export function XRobotMark({ className, glow = false }: { className?: string; glow?: boolean }) {
	return <img src={xRobotMark} alt="" aria-hidden="true" draggable={false} className={cn("xr-mark", glow && "xr-mark--glow", className)} />;
}

/** The product name in the brand's gradient type. */
export function XRobotWordmark({ className }: { className?: string }) {
	return <span className={cn("xr-wordmark font-bold tracking-tight", className)}>{BRAND_NAME}</span>;
}

type Satellite = { id: string; tone: "working" | "validating" | "idle" };

const satelliteFill: Record<Satellite["tone"], string> = {
	working: "var(--color-status-working)",
	validating: "var(--color-status-validating)",
	idle: "var(--muted-foreground)",
};

/**
 * Two crossing orbits around a glowing core. Each satellite stands for one
 * live run and carries its semantic status color; with none, the orbits are
 * empty. Purely visual: callers give it an accessible label or hide it.
 */
export function OrbitField({ satellites = [], className, label }: { satellites?: Satellite[]; className?: string; label?: string }) {
	const positions = [0, 180, 90, 270, 45, 225];
	return (
		<svg
			viewBox="0 0 200 200"
			className={className}
			role={label ? "img" : undefined}
			aria-label={label}
			aria-hidden={label ? undefined : true}
		>
			<defs>
				<radialGradient id="xr-core-glow" cx="50%" cy="50%" r="50%">
					<stop offset="0" stopColor="var(--brand-plasma, var(--primary))" stopOpacity="0.9" />
					<stop offset="1" stopColor="var(--brand-plasma, var(--primary))" stopOpacity="0" />
				</radialGradient>
				<linearGradient id="xr-ring" x1="0" y1="0" x2="1" y2="1">
					<stop offset="0" stopColor="var(--brand-nebula-teal, var(--primary))" />
					<stop offset="1" stopColor="var(--brand-nebula-violet, var(--primary))" />
				</linearGradient>
			</defs>
			<circle className="xr-core-pulse" cx="100" cy="100" r="44" fill="url(#xr-core-glow)" />
			<g className="xr-orbit-spin" fill="none" stroke="url(#xr-ring)" strokeWidth="1.6" opacity="0.85">
				<ellipse cx="100" cy="100" rx="88" ry="30" transform="rotate(38 100 100)" />
				<ellipse cx="100" cy="100" rx="88" ry="30" transform="rotate(-38 100 100)" />
			</g>
			<g className="xr-orbit-spin xr-orbit-spin--reverse">
				{satellites.slice(0, positions.length).map((satellite, index) => {
					const angle = (positions[index] * Math.PI) / 180;
					return (
						<circle
							key={satellite.id}
							data-testid="orbit-satellite"
							cx={100 + 72 * Math.cos(angle)}
							cy={100 + 72 * Math.sin(angle)}
							r="6.5"
							fill={satelliteFill[satellite.tone]}
							stroke="var(--background)"
							strokeWidth="2"
						/>
					);
				})}
			</g>
		</svg>
	);
}
