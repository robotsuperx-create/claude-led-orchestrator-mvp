import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Eye, ImageOff, LoaderCircle, TriangleAlert } from "lucide-react";
import type { ClaudeOrchestratorVisualRound, ClaudeOrchestratorVisualShot } from "../../shared/claude-orchestrator";
import { useClaudeOrchestratorVisual, useClaudeOrchestratorVisualShot } from "../hooks/useClaudeOrchestrator";
import { cn } from "../lib/utils";

const VIEWPORTS = ["desktop", "tablet", "phone"] as const;
const CRITERIA = ["hierarchy", "typography", "spacing", "colour", "imagery", "consistency", "responsiveness", "polish", "originality"] as const;

const viewportKey = {
	desktop: "orchestrator.visual.viewport.desktop",
	tablet: "orchestrator.visual.viewport.tablet",
	phone: "orchestrator.visual.viewport.phone",
} as const;

const criterionKey = {
	hierarchy: "orchestrator.visual.criterion.hierarchy",
	typography: "orchestrator.visual.criterion.typography",
	spacing: "orchestrator.visual.criterion.spacing",
	colour: "orchestrator.visual.criterion.colour",
	imagery: "orchestrator.visual.criterion.imagery",
	consistency: "orchestrator.visual.criterion.consistency",
	responsiveness: "orchestrator.visual.criterion.responsiveness",
	polish: "orchestrator.visual.criterion.polish",
	originality: "orchestrator.visual.criterion.originality",
} as const;

function useScore() {
	const { i18n } = useTranslation();
	const format = new Intl.NumberFormat(i18n.resolvedLanguage, { minimumFractionDigits: 1, maximumFractionDigits: 1 });
	return (value: number) => format.format(value);
}

function useViewportName() {
	const { t } = useTranslation();
	return (viewport: string) => (VIEWPORTS as readonly string[]).includes(viewport) ? t(viewportKey[viewport as (typeof VIEWPORTS)[number]]) : viewport;
}

/**
 * What Claude saw when it looked at the run's pages as an art director: the
 * score after each look, what it asked writers to change, and the screenshots
 * it judged, page by page at each screen size.
 */
export function VisualReview({ runId, live }: { runId: string; live: boolean }) {
	const { t } = useTranslation();
	const visual = useClaudeOrchestratorVisual(runId, live);
	const rounds = visual.data?.rounds ?? [];
	const [chosen, setChosen] = useState<number | null>(null);
	const formatScore = useScore();
	if (rounds.length === 0) return null;
	const judged = rounds.filter((round) => !round.failed);
	const selected = rounds.find((round) => round.round === chosen) ?? rounds[rounds.length - 1];
	return (
		<section className="mb-4 max-w-3xl" aria-labelledby={`${runId}-visual`} data-testid="claude-orchestrator-visual">
			<div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
				<h3 id={`${runId}-visual`} className="flex items-center gap-1.5 text-[13px] font-semibold text-foreground">
					<Eye className="size-3.5 text-muted-foreground" aria-hidden="true" />
					{t("orchestrator.visual.title")}
				</h3>
				{judged.length > 0 ? (
					<p className="xr-num text-[13px] text-muted-foreground" dir="ltr" data-testid="claude-orchestrator-visual-trail">
						{judged.map((round, index) => (
							<span key={round.round}>
								{index > 0 ? <span aria-hidden="true"> → </span> : null}
								<span className={cn(index === judged.length - 1 && "text-foreground")}>{t("orchestrator.visual.outOf", { score: formatScore(round.score) })}</span>
							</span>
						))}
					</p>
				) : null}
				{live ? <LoaderCircle className="size-3.5 animate-spin text-status-working motion-reduce:animate-none" aria-label={t("orchestrator.visual.looking")} /> : null}
			</div>
			<ol className="mt-2 overflow-hidden rounded-md border border-border">
				{rounds.map((round) => (
					<RoundRow key={round.round} round={round} selected={round.round === selected.round} onSelect={() => setChosen(round.round)} />
				))}
			</ol>
			{selected.shots.length > 0 ? <ShotViewer key={selected.round} runId={runId} shots={selected.shots} /> : null}
		</section>
	);
}

function RoundRow({ round, selected, onSelect }: { round: ClaudeOrchestratorVisualRound; selected: boolean; onSelect: () => void }) {
	const { t } = useTranslation();
	const formatScore = useScore();
	const criteria = CRITERIA.filter((name) => round.scores[name] !== undefined);
	return (
		<li className={cn("border-b border-border px-3 py-2.5 last:border-b-0", selected && "bg-muted/30")} data-testid="claude-orchestrator-visual-round">
			<button
				type="button"
				onClick={onSelect}
				aria-pressed={selected}
				disabled={round.shots.length === 0}
				className="flex w-full items-baseline gap-2 text-start focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 disabled:cursor-default"
			>
				<span className="text-[13px] font-medium text-foreground">{t("orchestrator.visual.round", { round: round.round })}</span>
				{round.failed ? (
					<span className="flex items-center gap-1 text-xs text-muted-foreground"><TriangleAlert className="size-3.5" aria-hidden="true" />{t("orchestrator.visual.failed")}</span>
				) : (
					<span className="xr-num text-[13px] text-foreground" dir="ltr">{t("orchestrator.visual.outOf", { score: formatScore(round.score) })}</span>
				)}
				{round.polished ? <span className="text-xs text-muted-foreground">{t("orchestrator.visual.polished")}</span> : null}
				{round.findings > 0 ? <span className="ms-auto text-[11px] text-muted-foreground">{t("orchestrator.visual.measured", { total: round.findings })}</span> : null}
			</button>
			{round.summary ? <p className="mt-1 max-w-prose text-xs leading-5 text-muted-foreground">{round.summary}</p> : null}
			{criteria.length > 0 ? (
				<p className="mt-1.5 flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] text-muted-foreground" data-testid="claude-orchestrator-visual-criteria">
					{criteria.map((name) => (
						<span key={name}>{t(criterionKey[name])} <span className="xr-num text-foreground" dir="ltr">{formatScore(round.scores[name])}</span></span>
					))}
				</p>
			) : null}
			{round.fixes.length > 0 ? (
				<div className="mt-2">
					<p className="text-[11px] font-medium text-foreground">{t("orchestrator.visual.fixes")}</p>
					<ol className="mt-1 list-decimal space-y-0.5 ps-5 text-xs leading-5 text-muted-foreground">
						{round.fixes.map((fix, index) => <li key={index}>{fix}</li>)}
					</ol>
				</div>
			) : null}
		</li>
	);
}

function ShotViewer({ runId, shots }: { runId: string; shots: ClaudeOrchestratorVisualShot[] }) {
	const { t } = useTranslation();
	const viewportName = useViewportName();
	const [index, setIndex] = useState(0);
	const shot = shots[Math.min(index, shots.length - 1)];
	return (
		<div className="mt-3" data-testid="claude-orchestrator-visual-shots">
			<div role="group" aria-label={t("orchestrator.visual.screenshots")} className="flex flex-wrap gap-2">
				{shots.map((item, position) => (
					<button
						key={`${item.page}-${item.viewport}-${position}`}
						type="button"
						aria-pressed={position === index}
						onClick={() => setIndex(position)}
						className={cn(
							"flex w-28 flex-col gap-1 rounded-md border p-1 text-start transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60 motion-reduce:transition-none",
							position === index ? "border-foreground/40 bg-muted/30" : "border-border hover:bg-muted/20",
						)}
						aria-label={t("orchestrator.visual.shotLabel", { page: item.page, viewport: viewportName(item.viewport) })}
					>
						<ShotImage runId={runId} name={item.name} className="h-16 w-full rounded-sm object-cover object-top" alt="" />
						<span className="px-0.5 text-[11px] leading-4 text-foreground">{viewportName(item.viewport)}</span>
						<span className="truncate px-0.5 font-mono text-[10px] leading-3 text-muted-foreground" dir="ltr" title={item.page}>{item.page}</span>
					</button>
				))}
			</div>
			<figure className="mt-2">
				<div className="max-h-[520px] overflow-auto rounded-md border border-border bg-muted/20">
					<ShotImage
						runId={runId}
						name={shot.name}
						className={cn("mx-auto block h-auto", shot.viewport === "phone" ? "w-[390px] max-w-full" : "w-full")}
						alt={t("orchestrator.visual.shotLabel", { page: shot.page, viewport: viewportName(shot.viewport) })}
					/>
				</div>
				{shot.findings.length > 0 ? (
					<figcaption className="mt-2">
						<ul className="space-y-0.5 text-[11px] leading-4 text-muted-foreground">
							{shot.findings.map((finding, position) => (
								<li key={position} className="flex gap-1.5"><TriangleAlert className="mt-px size-3 shrink-0 text-status-in-review" aria-hidden="true" />{finding}</li>
							))}
						</ul>
					</figcaption>
				) : null}
			</figure>
		</div>
	);
}

function ShotImage({ runId, name, className, alt }: { runId: string; name?: string; className: string; alt: string }) {
	const { t } = useTranslation();
	const picture = useClaudeOrchestratorVisualShot(runId, name);
	if (picture.data) return <img src={picture.data} alt={alt} className={className} draggable={false} />;
	if (picture.isLoading) {
		return (
			<span role="status" aria-label={t("orchestrator.visual.loading")} className="grid min-h-16 place-items-center text-muted-foreground">
				<LoaderCircle className="size-3.5 animate-spin motion-reduce:animate-none" aria-hidden="true" />
			</span>
		);
	}
	return (
		<span role="img" aria-label={t("orchestrator.visual.unavailable")} className="grid min-h-16 place-items-center text-muted-foreground">
			<ImageOff className="size-4" aria-hidden="true" />
		</span>
	);
}
