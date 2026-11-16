import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { claudeOrchestratorSiteUrl, type ClaudeOrchestratorLiveFile } from "../../../shared/claude-orchestrator";
import { useClaudeOrchestratorSitePages, useClaudeOrchestratorVisual, useClaudeOrchestratorVisualShot } from "../../hooks/useClaudeOrchestrator";

/** The screens the preview can show the page at, as the visual review photographs them. */
const DEVICES = [
	{ id: "desktop", width: "100%" },
	{ id: "tablet", width: "768px" },
	{ id: "phone", width: "390px" },
] as const;
type Device = (typeof DEVICES)[number]["id"];

/** A crew-written page runs scripts in its own origin, and nothing more: no top navigation, no popups, no forms. */
const FRAME_SANDBOX = "allow-scripts allow-same-origin";

/** At most one reload for this many milliseconds while the crew writes. */
const RELOAD_EVERY_MS = 3_000;

/** A signature of what the crew has written: when it changes, the page reloads. */
function revisionOf(files: ClaudeOrchestratorLiveFile[]): string {
	return files.map((file) => `${file.path}:${file.added}:${file.removed}`).join("|");
}

/**
 * The live preview under the crew map: the page the crew is building, open as
 * it is now, in a screen with an address bar, page tabs, screen sizes and a
 * reload. The page is served by the main process from the run's worktree on
 * its own origin; while the crew writes it reloads by itself. Before the run
 * has pages, the visual review's last photograph stands in.
 */
export function LivePreview({ runId, files, live }: { runId: string; files: ClaudeOrchestratorLiveFile[]; live: boolean }) {
	const { t } = useTranslation();
	const site = useClaudeOrchestratorSitePages(runId, live);
	const pages = site.data?.runId === runId ? site.data.pages : [];
	const [page, setPage] = useState<string | null>(null);
	const [device, setDevice] = useState<Device>("desktop");
	const [nonce, setNonce] = useState(0);
	const current = page && pages.includes(page) ? page : pages[0];
	const revision = useMemo(() => revisionOf(files), [files]);
	const [shownRevision, setShownRevision] = useState(revision);

	// Reload as the crew writes, but never faster than once every few seconds.
	useEffect(() => {
		if (revision === shownRevision) return;
		const timer = window.setTimeout(() => {
			setShownRevision(revision);
			setNonce((value) => value + 1);
		}, RELOAD_EVERY_MS);
		return () => window.clearTimeout(timer);
	}, [revision, shownRevision]);

	const width = DEVICES.find((item) => item.id === device)?.width ?? "100%";
	const address = current ? claudeOrchestratorSiteUrl(runId, current) : "";

	return (
		<section className="live-panel live-preview" aria-label={t("deck.preview.title")} data-testid="deck-live-preview">
			<header className="live-head">
				<span className={live && current ? "live-dot on" : "live-dot"} aria-hidden="true" />
				<h2 className="live-title">{t("deck.preview.title")}</h2>
				<span className="grow" />
				<div className="preview-devices" role="group" aria-label={t("deck.preview.screens")}>
					{DEVICES.map((item) => (
						<button key={item.id} type="button" aria-pressed={device === item.id} className={device === item.id ? "preview-device on" : "preview-device"} onClick={() => setDevice(item.id)}>
							{t(`deck.preview.${item.id}`)}
						</button>
					))}
				</div>
			</header>
			<div className="preview-chrome" dir="ltr">
				<span className="preview-lights" aria-hidden="true"><i /><i /><i /></span>
				<span className="preview-address" title={address}>{address || "—"}</span>
				<button type="button" className="preview-reload" onClick={() => setNonce((value) => value + 1)} disabled={!current} aria-label={t("deck.preview.reload")} title={t("deck.preview.reload")}>
					<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 11a8 8 0 1 0-2.3 5.7" /><path d="M20 4v7h-7" /></svg>
				</button>
			</div>
			{pages.length > 1 ? (
				<div className="preview-pages" role="tablist" aria-label={t("deck.preview.pages")}>
					{pages.map((name) => (
						<button key={name} type="button" role="tab" aria-selected={name === current} className={name === current ? "preview-page on" : "preview-page"} onClick={() => setPage(name)}>
							<bdi>{name}</bdi>
						</button>
					))}
				</div>
			) : null}
			<div className="preview-screen">
				{current ? (
					<div className="preview-device-frame" style={{ width }}>
						<iframe
							key={`${current}-${nonce}`}
							className="preview-frame"
							referrerPolicy="no-referrer"
							sandbox={FRAME_SANDBOX}
							src={address}
							title={t("deck.preview.frame")}
							data-testid="deck-preview-frame"
						/>
					</div>
				) : (
					<PreviewStandIn runId={runId} device={device} live={live} />
				)}
			</div>
		</section>
	);
}

/** Before the run has pages: the visual review's last photograph at the chosen size, or a word on what will appear. */
function PreviewStandIn({ runId, device, live }: { runId: string; device: Device; live: boolean }) {
	const { t } = useTranslation();
	const visual = useClaudeOrchestratorVisual(runId, live);
	const rounds = visual.data?.runId === runId ? visual.data.rounds : [];
	const last = [...rounds].reverse().find((round) => round.shots.some((shot) => shot.name));
	const shot = last?.shots.find((item) => item.viewport === device && item.name) ?? last?.shots.find((item) => item.name);
	const picture = useClaudeOrchestratorVisualShot(runId, shot?.name);
	if (shot && picture.data) {
		return (
			<figure className="preview-shot">
				<img src={picture.data} alt={t("deck.preview.shotAlt", { page: shot.page, screen: shot.viewport })} />
				<figcaption className="small t-2">{t("deck.preview.shot", { round: last?.round ?? 1 })}</figcaption>
			</figure>
		);
	}
	return <p className="preview-empty small t-2">{t("deck.preview.empty")}</p>;
}
