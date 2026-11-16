import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import type { ClaudeOrchestratorLiveFile, ClaudeOrchestratorLiveStep, ClaudeOrchestratorPlanStep } from "../../../shared/claude-orchestrator";
import { crewMember } from "../../lib/x00-crew";

const WIDTH = 1000;
const HEIGHT = 440;
const COMMANDER = { x: 92, y: HEIGHT / 2 };
const REPO_X = 905;
const NODE_R = 26;

type Node = { step: ClaudeOrchestratorLiveStep; x: number; y: number; depth: number };

/** Lays the plan out in columns by dependency depth: X-00 on the left, the repository on the right. */
export function layoutCrewMap(steps: ClaudeOrchestratorLiveStep[], plan: ClaudeOrchestratorPlanStep[]): { nodes: Node[]; edges: Array<[Node, Node]> } {
	const depends = new Map(plan.map((step) => [step.id, step.dependsOn]));
	const depthOf = new Map<string, number>();
	const visit = (id: string, trail: Set<string>): number => {
		const known = depthOf.get(id);
		if (known !== undefined) return known;
		if (trail.has(id)) return 0;
		trail.add(id);
		const parents = (depends.get(id) ?? []).filter((parent) => steps.some((step) => step.id === parent));
		const depth = parents.length === 0 ? 0 : Math.max(...parents.map((parent) => visit(parent, trail))) + 1;
		depthOf.set(id, depth);
		return depth;
	};
	for (const step of steps) visit(step.id, new Set());
	const columns = Math.max(1, ...[...depthOf.values()].map((depth) => depth + 1));
	const left = 230;
	const right = 760;
	const nodes: Node[] = [];
	for (let depth = 0; depth < columns; depth++) {
		const column = steps.filter((step) => (depthOf.get(step.id) ?? 0) === depth);
		const x = columns === 1 ? (left + right) / 2 : left + ((right - left) * depth) / (columns - 1);
		column.forEach((step, row) => {
			const y = column.length === 1 ? HEIGHT / 2 : 70 + ((HEIGHT - 140) * row) / (column.length - 1);
			nodes.push({ step, x, y, depth });
		});
	}
	const byId = new Map(nodes.map((node) => [node.step.id, node]));
	const edges: Array<[Node, Node]> = [];
	for (const node of nodes) {
		for (const parent of depends.get(node.step.id) ?? []) {
			const from = byId.get(parent);
			if (from) edges.push([from, node]);
		}
	}
	return { nodes, edges };
}

function curve(x1: number, y1: number, x2: number, y2: number): string {
	const bend = (x2 - x1) * 0.45;
	return `M ${x1} ${y1} C ${x1 + bend} ${y1}, ${x2 - bend} ${y2}, ${x2} ${y2}`;
}

/**
 * The crew map: X-00 sends the plan out to the crew, each step a node carried
 * by its crew member, linked by what it waits for, and the code flows on to
 * the repository. Light tells the state: ion pulses while a step works, long
 * green when done, veto red when it failed, dashed ivory when blocked. Hover
 * or focus a node to read it; select one to pin it.
 */
export function AgentMap({ steps, plan, files, live }: { steps: ClaudeOrchestratorLiveStep[]; plan: ClaudeOrchestratorPlanStep[]; files: ClaudeOrchestratorLiveFile[]; live: boolean }) {
	const { t } = useTranslation();
	const { nodes, edges } = useMemo(() => layoutCrewMap(steps, plan), [steps, plan]);
	const [hover, setHover] = useState<string | null>(null);
	const [pinned, setPinned] = useState<string | null>(null);
	const focus = nodes.find((node) => node.step.id === (hover ?? pinned)) ?? nodes.find((node) => node.step.state === "running");
	const repoFiles = files.slice(0, 5);
	const writing = steps.some((step) => step.state === "running");

	if (nodes.length === 0) {
		return (
			<section className="live-panel crew-map" aria-label={t("deck.map.title")} data-testid="deck-crew-map">
				<header className="live-head">
					<span className="live-dot" aria-hidden="true" />
					<h2 className="live-title">{t("deck.map.title")}</h2>
				</header>
				<p className="map-empty small t-2">{t("deck.map.empty")}</p>
			</section>
		);
	}

	return (
		<section className="live-panel crew-map" aria-label={t("deck.map.title")} data-testid="deck-crew-map" data-live={live}>
			<header className="live-head">
				<span className={live ? "live-dot on" : "live-dot"} aria-hidden="true" />
				<h2 className="live-title">{t("deck.map.title")}</h2>
				<span className="live-meta small t-2">
					{t("deck.map.summary", { done: steps.filter((step) => step.state === "done").length, total: steps.length })}
				</span>
			</header>
			<div className="map-stage" dir="ltr">
				<svg viewBox={`0 0 ${WIDTH} ${HEIGHT}`} role="group" aria-label={t("deck.map.title")} className="map-svg">
					<defs>
						<pattern id="xrd-grid" width="40" height="40" patternUnits="userSpaceOnUse">
							<path d="M 40 0 L 0 0 0 40" className="map-grid" />
						</pattern>
						<radialGradient id="xrd-core" cx="50%" cy="50%" r="50%">
							<stop offset="0%" stopColor="var(--ion)" stopOpacity="0.55" />
							<stop offset="100%" stopColor="var(--ion)" stopOpacity="0" />
						</radialGradient>
						{nodes.map((node) => (
							<clipPath id={`xrd-face-${node.step.index}`} key={node.step.id}>
								<circle cx={node.x} cy={node.y} r={NODE_R - 3} />
							</clipPath>
						))}
					</defs>
					<rect width={WIDTH} height={HEIGHT} fill="url(#xrd-grid)" />
					<line x1="0" x2={WIDTH} y1="0" y2="0" className="map-scan" />

					{/* X-00 sends each first step out; the others wait on their parents. */}
					{nodes.filter((node) => node.depth === 0).map((node) => (
						<path key={`c-${node.step.id}`} id={`xrd-c-${node.step.index}`} d={curve(COMMANDER.x + 34, COMMANDER.y, node.x - NODE_R, node.y)} className={`map-edge ${node.step.state}`} />
					))}
					{edges.map(([from, to]) => (
						<path key={`e-${from.step.id}-${to.step.id}`} id={`xrd-e-${from.step.index}-${to.step.index}`} d={curve(from.x + NODE_R, from.y, to.x - NODE_R, to.y)} className={`map-edge ${to.step.state}`} />
					))}
					{/* Code flows from the steps at work to the repository. */}
					{nodes.filter((node) => node.step.state === "running" || node.step.state === "done").map((node) => (
						<path key={`r-${node.step.id}`} id={`xrd-r-${node.step.index}`} d={curve(node.x + NODE_R, node.y, REPO_X - 40, HEIGHT / 2)} className={`map-edge to-repo ${node.step.state}`} />
					))}
					{live
						? nodes.filter((node) => node.step.state === "running").map((node) => (
							<g key={`p-${node.step.id}`} className="map-packets">
								{[0, 1, 2].map((n) => (
									<circle key={n} r="3.2" className="map-packet">
										<animateMotion dur="2.4s" begin={`${n * 0.8}s`} repeatCount="indefinite">
											<mpath href={`#xrd-r-${node.step.index}`} />
										</animateMotion>
									</circle>
								))}
							</g>
						))
						: null}

					<g className="map-commander">
						<circle cx={COMMANDER.x} cy={COMMANDER.y} r="70" fill="url(#xrd-core)" className={writing ? "map-core on" : "map-core"} />
						<polygon points={hexagon(COMMANDER.x, COMMANDER.y, 34)} className="map-hex" />
						<text x={COMMANDER.x} y={COMMANDER.y + 5} className="map-callsign big">X-00</text>
						<text x={COMMANDER.x} y={COMMANDER.y + 56} className="map-label">{t("deck.crew.commander")}</text>
					</g>

					{nodes.map((node) => {
						const member = crewMember(node.step.index);
						const selected = focus?.step.id === node.step.id;
						return (
							<g
								key={node.step.id}
								className={`map-node ${node.step.state}${selected ? " selected" : ""}`}
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
								<circle cx={node.x} cy={node.y} r={NODE_R + 9} className="map-ring" />
								<circle cx={node.x} cy={node.y} r={NODE_R} className="map-disc" />
								{member.portrait ? (
									<image href={member.portrait} x={node.x - NODE_R} y={node.y - NODE_R} width={NODE_R * 2} height={NODE_R * 2} clipPath={`url(#xrd-face-${node.step.index})`} className="map-face" preserveAspectRatio="xMidYMid slice" />
								) : null}
								<text x={node.x} y={node.y + NODE_R + 22} className="map-callsign">{member.callSign}</text>
							</g>
						);
					})}

					<g className="map-repo">
						<rect x={REPO_X - 40} y={HEIGHT / 2 - 34} width="80" height="68" rx="2" className="map-repo-box" />
						<text x={REPO_X} y={HEIGHT / 2 + 5} className="map-callsign">{"</>"}</text>
						<text x={REPO_X} y={HEIGHT / 2 + 56} className="map-label">{t("deck.map.repo")}</text>
						{repoFiles.map((file, row) => (
							<text key={file.path} x={REPO_X} y={HEIGHT / 2 + 84 + row * 18} className="map-file">
								{shortPath(file.path)} +{file.added}
							</text>
						))}
					</g>
				</svg>
				{focus ? (
					<div className="map-card" data-testid="deck-map-card">
						<span className="map-card-who">{crewMember(focus.step.index).callSign}</span>
						<span className="map-card-title" dir="auto">{focus.step.title}</span>
						<span className={`map-card-state ${focus.step.state}`}>
							{t(`deck.map.state.${focus.step.state}`)}
							{focus.step.attempt > 1 ? ` · ${t("deck.map.attempt", { n: focus.step.attempt })}` : ""}
						</span>
					</div>
				) : null}
			</div>
		</section>
	);
}

function hexagon(cx: number, cy: number, r: number): string {
	return Array.from({ length: 6 }, (_, i) => {
		const angle = (Math.PI / 3) * i + Math.PI / 6;
		return `${(cx + r * Math.cos(angle)).toFixed(1)},${(cy + r * Math.sin(angle)).toFixed(1)}`;
	}).join(" ");
}

function shortPath(path: string): string {
	const name = path.split("/").pop() ?? path;
	return name.length > 18 ? `${name.slice(0, 17)}…` : name;
}
