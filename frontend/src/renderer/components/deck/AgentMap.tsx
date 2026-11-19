import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { ClaudeOrchestratorLiveFile, ClaudeOrchestratorLiveStep, ClaudeOrchestratorPlanStep } from "../../../shared/claude-orchestrator";
import { crewMember } from "../../lib/x00-crew";

const W = 1000;
const H = 620;
const CORE = { x: 470, y: 310 };
const VAULT = { x: 905, y: 310 };
const NODE_R = 25;

export type CrewMapNode = { step: ClaudeOrchestratorLiveStep; x: number; y: number; angle: number };

/**
 * The crew in orbit around X-00: one ring for a small plan, two for a large
 * one, the first step at the top and the rest clockwise in plan order.
 */
export function layoutCrewMap(steps: ClaudeOrchestratorLiveStep[], plan: ClaudeOrchestratorPlanStep[]): { nodes: CrewMapNode[]; edges: Array<[CrewMapNode, CrewMapNode]> } {
	const ordered = [...steps].sort((a, b) => a.index - b.index);
	const inner = ordered.length > 8 ? Math.ceil(ordered.length / 2) : ordered.length;
	const nodes = ordered.map((step, i) => {
		const outer = i >= inner;
		const count = outer ? ordered.length - inner : inner;
		const place = outer ? i - inner : i;
		const radius = outer ? 250 : ordered.length > 8 ? 165 : 210;
		const angle = -Math.PI / 2 + (place / count) * Math.PI * 2 + (outer ? Math.PI / count : 0);
		return { step, angle, x: CORE.x + radius * Math.cos(angle), y: CORE.y + radius * Math.sin(angle) };
	});
	const byId = new Map(nodes.map((node) => [node.step.id, node]));
	const edges: Array<[CrewMapNode, CrewMapNode]> = [];
	for (const planStep of plan) {
		const to = byId.get(planStep.id);
		for (const parent of planStep.dependsOn) {
			const from = byId.get(parent);
			if (from && to) edges.push([from, to]);
		}
	}
	return { nodes, edges };
}

/** A beam from one point to another, bowed a little toward the core. */
function arc(x1: number, y1: number, x2: number, y2: number, pull = 0.35): string {
	const mx = (x1 + x2) / 2;
	const my = (y1 + y2) / 2;
	const cx = mx + (CORE.x - mx) * pull;
	const cy = my + (CORE.y - my) * pull;
	return `M ${x1.toFixed(1)} ${y1.toFixed(1)} Q ${cx.toFixed(1)} ${cy.toFixed(1)} ${x2.toFixed(1)} ${y2.toFixed(1)}`;
}

function polygon(cx: number, cy: number, r: number, sides: number, turn = 0): string {
	return Array.from({ length: sides }, (_, i) => {
		const a = turn + (Math.PI * 2 * i) / sides;
		return `${(cx + r * Math.cos(a)).toFixed(1)},${(cy + r * Math.sin(a)).toFixed(1)}`;
	}).join(" ");
}

/** A reticle's four corners around a point. */
function reticle(cx: number, cy: number, r: number): string {
	const k = r * 0.42;
	return [
		`M ${cx - r} ${cy - r + k} V ${cy - r} H ${cx - r + k}`,
		`M ${cx + r - k} ${cy - r} H ${cx + r} V ${cy - r + k}`,
		`M ${cx + r} ${cy + r - k} V ${cy + r} H ${cx + r - k}`,
		`M ${cx - r + k} ${cy + r} H ${cx - r} V ${cy + r - k}`,
	].join(" ");
}

function shortText(text: string, max: number): string {
	return text.length > max ? `${text.slice(0, max - 1)}…` : text;
}

/** A fake but stable memory address for a file, as the HUD shows it. */
function address(path: string): string {
	let hash = 2166136261;
	for (const ch of path) hash = Math.imul(hash ^ ch.charCodeAt(0), 16777619);
	return `0x${(hash >>> 0).toString(16).toUpperCase().padStart(8, "0")}`;
}

function useReducedMotion(): boolean {
	const [reduced, setReduced] = useState(() => typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches);
	useEffect(() => {
		if (typeof window.matchMedia !== "function") return;
		const query = window.matchMedia("(prefers-reduced-motion: reduce)");
		const change = () => setReduced(query.matches);
		query.addEventListener?.("change", change);
		return () => query.removeEventListener?.("change", change);
	}, []);
	return reduced;
}

/**
 * The crew map as an AI operations grid on the deck's black: X-00 is the core,
 * its fine rings turning slowly; the crew orbit it, each on
 * their plan step, linked by what they wait for. Orders travel out along the
 * beams to the members at work, code travels on to the vault, and every node
 * wears its state: a turning reticle while it works, green when done, red and
 * glitching when it failed, locked when blocked. Hover or focus a member to
 * read them; a click pins them.
 */
export function AgentMap({ steps, plan, files, live }: { steps: ClaudeOrchestratorLiveStep[]; plan: ClaudeOrchestratorPlanStep[]; files: ClaudeOrchestratorLiveFile[]; live: boolean }) {
	const { t } = useTranslation();
	const reduced = useReducedMotion();
	const { nodes, edges } = useMemo(() => layoutCrewMap(steps, plan), [steps, plan]);
	const [hover, setHover] = useState<string | null>(null);
	const [pinned, setPinned] = useState<string | null>(null);
	const focus = nodes.find((node) => node.step.id === (hover ?? pinned)) ?? nodes.find((node) => node.step.state === "running");
	const done = steps.filter((step) => step.state === "done").length;
	const working = nodes.filter((node) => node.step.state === "running");
	const motion = live && !reduced;
	const vaultFiles = files.slice(0, 6);
	const lines = files.reduce((sum, file) => sum + file.added, 0);

	return (
		<section className={live ? "crew-map on" : "crew-map"} aria-label={t("deck.map.title")} data-testid="deck-crew-map" data-live={live}>
			<header className="grid-head">
				<span className={live ? "live-dot on" : "live-dot"} aria-hidden="true" />
				<h2 className="live-title">{t("deck.map.title")}</h2>
				<span className="grid-telemetry" dir="ltr">
					<span>X-00</span>
					<span>{done}/{steps.length}</span>
					<span>{working.length > 0 ? `▲ ${working.length}` : "■"}</span>
					<span>{files.length} · {lines}</span>
				</span>
			</header>
			{nodes.length === 0 ? (
				<div className="grid-stage grid-stage--empty" dir="ltr">
					<p className="map-empty small t-2">{t("deck.map.empty")}</p>
				</div>
			) : (
				<div className="grid-stage" dir="ltr">
					<i className="grid-corner tl" aria-hidden="true" />
					<i className="grid-corner tr" aria-hidden="true" />
					<i className="grid-corner bl" aria-hidden="true" />
					<i className="grid-corner br" aria-hidden="true" />
					<svg viewBox={`0 0 ${W} ${H}`} role="group" aria-label={t("deck.map.title")} className="grid-svg">
						<defs>
							<pattern id="xrd-dots" width="22" height="22" patternUnits="userSpaceOnUse">
								<circle cx="1.5" cy="1.5" r="1" className="grid-dot" />
							</pattern>
							<radialGradient id="xrd-core-glow" cx="50%" cy="50%" r="50%">
								<stop offset="0%" stopColor="#C6E3F0" stopOpacity="0.5" />
								<stop offset="45%" stopColor="#C6E3F0" stopOpacity="0.1" />
								<stop offset="100%" stopColor="#C6E3F0" stopOpacity="0" />
							</radialGradient>
							<radialGradient id="xrd-field" cx="47%" cy="50%" r="55%">
								<stop offset="0%" stopColor="#ffffff" stopOpacity="1" />
								<stop offset="100%" stopColor="#ffffff" stopOpacity="0" />
							</radialGradient>
							<mask id="xrd-field-mask">
								<rect width={W} height={H} fill="url(#xrd-field)" />
							</mask>
							{nodes.map((node) => (
								<clipPath id={`xrd-face-${node.step.index}`} key={node.step.id}>
									<circle cx={node.x} cy={node.y} r={NODE_R - 4} />
								</clipPath>
							))}
						</defs>

						{/* the field: a dot matrix fading into the black, and the orbits with their range ticks */}
						<rect width={W} height={H} fill="url(#xrd-dots)" mask="url(#xrd-field-mask)" />
						{[165, 210, 250, 300].map((r) => (
							<circle key={r} cx={CORE.x} cy={CORE.y} r={r} className={r === 210 || r === 250 ? "grid-orbit main" : "grid-orbit"} />
						))}
						<circle cx={CORE.x} cy={CORE.y} r="300" className="grid-ticks" />
						<circle cx={CORE.x} cy={CORE.y} r="300" className="grid-ticks major" />

						{/* orders out from the core */}
						{nodes.map((node) => (
							<path key={`b-${node.step.id}`} id={`xrd-b-${node.step.index}`} d={arc(CORE.x, CORE.y, node.x, node.y, 0)} className={`grid-beam ${node.step.state}`} />
						))}
						{/* what each step waits for */}
						{edges.map(([from, to]) => (
							<path key={`e-${from.step.id}-${to.step.id}`} d={arc(from.x, from.y, to.x, to.y, 0.45)} className={`grid-link ${to.step.state}`} />
						))}
						{/* code on its way to the vault */}
						{nodes.filter((node) => node.step.state === "running" || node.step.state === "done").map((node) => (
							<path key={`v-${node.step.id}`} id={`xrd-v-${node.step.index}`} d={arc(node.x, node.y, VAULT.x - 36, VAULT.y, -0.25)} className={`grid-stream ${node.step.state}`} />
						))}
						{motion
							? working.map((node) => (
								<g key={`p-${node.step.id}`}>
									{[0, 1].map((n) => (
										<circle key={`o${n}`} r="1.8" className="grid-packet order">
											<animateMotion dur="3.2s" begin={`${n * 1.6}s`} repeatCount="indefinite">
												<mpath href={`#xrd-b-${node.step.index}`} />
											</animateMotion>
										</circle>
									))}
									{[0, 1, 2].map((n) => (
										<rect key={`c${n}`} width="4" height="1.5" className="grid-packet code">
											<animateMotion dur="3.6s" begin={`${n * 1.2}s`} repeatCount="indefinite" rotate="auto">
												<mpath href={`#xrd-v-${node.step.index}`} />
											</animateMotion>
										</rect>
									))}
								</g>
							))
							: null}

						{/* X-00, the core */}
						<g className={working.length > 0 ? "grid-core on" : "grid-core"}>
							<circle cx={CORE.x} cy={CORE.y} r="130" fill="url(#xrd-core-glow)" className="grid-core-glow" />
							<circle cx={CORE.x} cy={CORE.y} r="82" className="grid-core-ring outer" />
							<circle cx={CORE.x} cy={CORE.y} r="68" className="grid-core-ring inner" />
							<circle cx={CORE.x} cy={CORE.y} r="58" className="grid-core-ring thin" />
							<polygon points={polygon(CORE.x, CORE.y, 46, 6, Math.PI / 6)} className="grid-core-hex" />
							<polygon points={polygon(CORE.x, CORE.y, 32, 6, Math.PI / 6)} className="grid-core-hex inner" />
							<circle cx={CORE.x} cy={CORE.y} r="4" className="grid-core-heart" />
							<text x={CORE.x} y={CORE.y + 22} className="grid-core-name">X-00</text>
							<text x={CORE.x} y={CORE.y + 112} className="grid-core-label">{t("deck.crew.commander")}</text>
						</g>

						{/* the crew */}
						{nodes.map((node) => {
							const member = crewMember(node.step.index);
							const selected = focus?.step.id === node.step.id;
							// The tag sits beside the member, on the side away from the core.
							const outward = Math.cos(node.angle) >= -0.01;
							const lx = node.x + (outward ? 46 : -46);
							const ly = node.y + Math.sin(node.angle) * 8 - 4;
							return (
								<g
									key={node.step.id}
									className={`grid-node ${node.step.state}${selected ? " selected" : ""}`}
									tabIndex={0}
									role="button"
									aria-pressed={pinned === node.step.id}
									aria-label={`${member.callSign} · ${node.step.title} · ${t(`deck.map.state.${node.step.state}`)}`}
									onMouseEnter={() => setHover(node.step.id)}
									onMouseLeave={() => setHover(null)}
									onFocus={() => setHover(node.step.id)}
									onBlur={() => setHover(null)}
									onClick={() => setPinned((current) => (current === node.step.id ? null : node.step.id))}
									onKeyDown={(event) => {
										if (event.key === "Enter" || event.key === " ") {
											event.preventDefault();
											setPinned((current) => (current === node.step.id ? null : node.step.id));
										}
									}}
									data-state={node.step.state}
									data-testid={`deck-map-node-${node.step.index}`}
								>
									<circle cx={node.x} cy={node.y} r={NODE_R + 16} className="grid-node-halo" />
									<path d={reticle(node.x, node.y, NODE_R + 10)} className="grid-reticle" style={{ transformOrigin: `${node.x}px ${node.y}px` }} />
									<polygon points={polygon(node.x, node.y, NODE_R + 3, 6, Math.PI / 6)} className="grid-node-frame" />
									<circle cx={node.x} cy={node.y} r={NODE_R - 4} className="grid-node-disc" />
									{member.portrait ? (
										<image href={member.portrait} x={node.x - NODE_R} y={node.y - NODE_R} width={NODE_R * 2} height={NODE_R * 2} clipPath={`url(#xrd-face-${node.step.index})`} className="grid-face" preserveAspectRatio="xMidYMid slice" />
									) : null}
									<g className="grid-tag" textAnchor={outward ? "start" : "end"}>
										<text x={lx} y={ly - 6} className="grid-callsign">{member.callSign}</text>
										<text x={lx} y={ly + 8} className="grid-state">{t(`deck.map.state.${node.step.state}`)}{node.step.attempt > 1 ? ` ×${node.step.attempt}` : ""}</text>
										<text x={lx} y={ly + 22} className="grid-step">{shortText(node.step.title, 28)}</text>
									</g>
								</g>
							);
						})}

						{/* the vault: the repository the code lands in */}
						<g className={files.length > 0 ? "grid-vault on" : "grid-vault"}>
							<polygon points={polygon(VAULT.x, VAULT.y, 38, 6, 0)} className="grid-vault-frame" />
							<polygon points={polygon(VAULT.x, VAULT.y, 28, 6, 0)} className="grid-vault-core" />
							<text x={VAULT.x} y={VAULT.y + 5} className="grid-vault-glyph">{"</>"}</text>
							<text x={VAULT.x} y={VAULT.y + 60} className="grid-core-label">{t("deck.map.repo")}</text>
							{vaultFiles.map((file, row) => (
								<text key={file.path} x={VAULT.x + 56} y={VAULT.y + 84 + row * 16} className="grid-file" textAnchor="end">
									<tspan className="grid-file-addr">{address(file.path)}</tspan> {shortText(file.path.split("/").pop() ?? file.path, 16)} +{file.added}
								</text>
							))}
						</g>
					</svg>
					{focus ? (
						<div className={`grid-card ${focus.step.state}`} data-testid="deck-map-card">
							<span className="grid-card-who">{crewMember(focus.step.index).callSign}</span>
							<span className="grid-card-title" dir="auto">{focus.step.title}</span>
							<span className="grid-card-state">
								{t(`deck.map.state.${focus.step.state}`)}
								{focus.step.attempt > 1 ? ` · ${t("deck.map.attempt", { n: focus.step.attempt })}` : ""}
							</span>
							{focus.step.failure && focus.step.state !== "done" ? (
								<span className="grid-card-failure" dir="auto" title={focus.step.failure} data-testid="deck-map-failure">
									{t("deck.map.lastFailure", { reason: focus.step.failure })}
								</span>
							) : null}
						</div>
					) : null}
				</div>
			)}
		</section>
	);
}
