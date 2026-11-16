import { useEffect, useMemo, useRef, useState, type ChangeEvent } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { MEDIA_MAX_PROMPT_CHARS, MEDIA_MAX_UPLOAD_BYTES, type MediaCapability, type MediaItem } from "../../../shared/media";
import { useUiStore } from "../../stores/ui-store";
import { mediaErrorCode, mediaUrl, useCreateMedia, useMediaInfo, useMediaItems, useMediaServerStatus, useRemoveMedia, useUploadMedia } from "../../hooks/useMedia";

/** Picture shapes the composer offers, as the stores and reels use them. */
const SHAPES = [
	{ id: "square", width: 1024, height: 1024 },
	{ id: "portrait", width: 768, height: 1344 },
	{ id: "landscape", width: 1344, height: 768 },
] as const;
type Shape = (typeof SHAPES)[number]["id"];

const BATCHES = [1, 4, 10, 30] as const;

/** The night queue's hour: the next 02:00 on this computer's clock. */
export function nextNight(now = new Date()): Date {
	const night = new Date(now);
	night.setHours(2, 0, 0, 0);
	if (night.getTime() <= now.getTime()) night.setDate(night.getDate() + 1);
	return night;
}

function hours(seconds?: number): string {
	return ((seconds ?? 0) / 3600).toFixed(1);
}

function gigabytes(bytes?: number): string {
	return bytes ? `${Math.round(bytes / 1024 ** 3)} GB` : "";
}

/** The GPU server's line: connected and on which card, or what is missing. */
function ServerLine() {
	const { t } = useTranslation();
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const info = useMediaInfo();
	const enabled = info.data?.enabled === true;
	const status = useMediaServerStatus(enabled);
	if (!enabled) {
		return (
			<p className="media-server" data-testid="media-server">
				<span className="live-dot" aria-hidden="true" />
				<span>{t(`deck.media.off.${info.data?.reason ?? "no_server"}`)}</span>
				<button type="button" className="btn btn-text" onClick={() => openGlobalSettings("missionControl")}>{t("deck.media.openSettings")}</button>
			</p>
		);
	}
	const reachable = status.data?.reachable === true;
	return (
		<p className="media-server" data-testid="media-server">
			<span className={reachable ? "live-dot on" : "live-dot"} aria-hidden="true" />
			<span>{reachable ? t("deck.media.connected") : t("deck.media.unreachable")}</span>
			{reachable && status.data?.device ? <bdi className="t-2">{[status.data.device, gigabytes(status.data.vramBytes)].filter(Boolean).join(" · ")}</bdi> : null}
			{reachable && status.data?.queue ? <span className="t-2">{t("deck.media.queue", { count: status.data.queue })}</span> : null}
			{info.data?.voice ? <span className="t-2">{t("deck.media.voiceOn")}</span> : null}
			<span className="media-meter t-2" data-testid="media-meter">
				{t("deck.media.meter", { today: hours(status.data?.onSecondsToday), month: hours(status.data?.onSecondsMonth) })}
				{status.data?.pricePerHour ? ` · ${t("deck.media.cost", { today: (status.data.costToday ?? 0).toFixed(2), month: (status.data.costMonth ?? 0).toFixed(2) })}` : ""}
			</span>
			{status.data?.autoPower ? <span className="t-2">{t("deck.media.autoStop", { minutes: status.data.idleMinutes ?? 0 })}</span> : null}
			{status.data?.scheduled ? <span className="t-2">{t("deck.media.waiting", { count: status.data.scheduled })}</span> : null}
		</p>
	);
}

const BUILT_IN_NAMES = {
	image: "deck.media.kind.image",
	video: "deck.media.kind.video",
	music: "deck.media.kind.music",
	upscale: "deck.media.kind.upscale",
	restyle: "deck.media.kind.restyle",
	animate: "deck.media.kind.animate",
} as const;

/** The ad pack: from one product picture, a new scene in the three shapes the stores and reels use, and a short video. */
const AD_PACK = [
	{ capability: "restyle", width: 768, height: 1344 },
	{ capability: "restyle", width: 1024, height: 1024 },
	{ capability: "restyle", width: 1344, height: 768 },
	{ capability: "animate", seconds: 4 },
] as const;

function capabilityName(t: TFunction, capability: MediaCapability): string {
	const key = capability.builtIn ? BUILT_IN_NAMES[capability.id as keyof typeof BUILT_IN_NAMES] : undefined;
	return key ? t(key) : capability.title || capability.id;
}

/** What to make: the kind, the words, the shape or length, and a picture to start from. */
function Composer({ capabilities, input, onClearInput }: { capabilities: MediaCapability[]; input: MediaItem | null; onClearInput: () => void }) {
	const { t } = useTranslation();
	const create = useCreateMedia();
	const upload = useUploadMedia();
	const fileRef = useRef<HTMLInputElement>(null);
	const usable = useMemo(() => capabilities.filter((capability) => (input ? capability.input === "image" : !capability.input)), [capabilities, input]);
	const [chosen, setChosen] = useState<string>("");
	const capability = usable.find((item) => item.id === chosen) ?? usable[0];
	const [prompt, setPrompt] = useState("");
	const [shape, setShape] = useState<Shape>("square");
	const [seconds, setSeconds] = useState(4);
	const [batch, setBatch] = useState<number>(1);
	const [tonight, setTonight] = useState(false);
	const error = mediaErrorCode(create.error) ?? mediaErrorCode(upload.error);

	useEffect(() => {
		if (capability && chosen !== capability.id) setChosen(capability.id);
	}, [capability, chosen]);

	// A tool working on a picture may need no words: the picture's own stand in.
	const words = prompt.trim() || (input ? input.prompt.trim() || capability?.id || "" : "");
	const canPack = Boolean(input) && AD_PACK.every((step) => capabilities.some((item) => item.id === step.capability && item.input === "image"));

	const submit = () => {
		if (!capability || !words) return;
		const size = SHAPES.find((item) => item.id === shape) ?? SHAPES[0];
		create.mutate(
			{
				capability: capability.id,
				prompt: words,
				...(capability.kind === "image" && !capability.input ? { width: size.width, height: size.height } : {}),
				...(capability.kind === "video" ? { seconds } : {}),
				...(capability.kind === "audio" ? { seconds: Math.min(47, seconds * 5) } : {}),
				...(input ? { input: input.id } : {}),
				...(batch > 1 ? { count: batch } : {}),
				...(tonight ? { runAt: nextNight().toISOString() } : {}),
			},
			{ onSuccess: () => setPrompt("") },
		);
	};

	const pack = () => {
		if (!input || !prompt.trim()) return;
		for (const step of AD_PACK) create.mutate({ ...step, prompt: prompt.trim(), input: input.id });
		setPrompt("");
	};

	const pick = async (event: ChangeEvent<HTMLInputElement>) => {
		const file = event.target.files?.[0];
		event.target.value = "";
		if (!file || file.size > MEDIA_MAX_UPLOAD_BYTES) return;
		upload.mutate({ name: file.name, data: new Uint8Array(await file.arrayBuffer()) });
	};

	return (
		<div className="media-composer" data-testid="media-composer">
			<div className="media-kinds" role="tablist" aria-label={t("deck.media.kinds")}>
				{usable.map((item) => (
					<button key={item.id} type="button" role="tab" aria-selected={item.id === capability?.id} className={item.id === capability?.id ? "media-kind on" : "media-kind"} onClick={() => setChosen(item.id)}>
						{capabilityName(t, item)}
					</button>
				))}
			</div>
			{input ? (
				<p className="media-input small">
					<span className="t-2">{t("deck.media.from")}</span>
					<bdi dir="auto">{input.prompt}</bdi>
					<button type="button" className="btn btn-text" onClick={onClearInput}>{t("deck.media.clearInput")}</button>
				</p>
			) : null}
			<textarea
				className="media-prompt"
				dir="auto"
				rows={3}
				maxLength={MEDIA_MAX_PROMPT_CHARS}
				value={prompt}
				placeholder={input ? t("deck.media.placeholder.input") : t(`deck.media.placeholder.${capability?.kind ?? "image"}`)}
				aria-label={t("deck.media.prompt")}
				onChange={(event) => setPrompt(event.target.value)}
				onKeyDown={(event) => {
					if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) submit();
				}}
				data-testid="media-prompt"
			/>
			<div className="media-row">
				{capability?.kind === "image" && !capability.input ? (
					<div className="media-shapes" role="group" aria-label={t("deck.media.shape")}>
						{SHAPES.map((item) => (
							<button key={item.id} type="button" aria-pressed={shape === item.id} className={shape === item.id ? "media-kind on" : "media-kind"} onClick={() => setShape(item.id)}>
								{t(`deck.media.shapes.${item.id}`)}
							</button>
						))}
					</div>
				) : null}
				{capability && capability.kind !== "image" ? (
					<label className="media-seconds small">
						<span className="t-2">{t("deck.media.length")}</span>
						<input type="range" min={1} max={capability.kind === "video" ? 10 : 9} value={seconds} onChange={(event) => setSeconds(Number(event.target.value))} />
						<span className="n">{t("deck.media.seconds", { count: capability.kind === "video" ? seconds : Math.min(47, seconds * 5) })}</span>
					</label>
				) : null}
				<div className="media-shapes" role="group" aria-label={t("deck.media.count")}>
					{BATCHES.map((value) => (
						<button key={value} type="button" aria-pressed={batch === value} className={batch === value ? "media-kind on" : "media-kind"} onClick={() => setBatch(value)}>
							<span className="n">×{value}</span>
						</button>
					))}
				</div>
				<div className="media-shapes" role="group" aria-label={t("deck.media.when")}>
					<button type="button" aria-pressed={!tonight} className={!tonight ? "media-kind on" : "media-kind"} onClick={() => setTonight(false)}>{t("deck.media.now")}</button>
					<button type="button" aria-pressed={tonight} className={tonight ? "media-kind on" : "media-kind"} onClick={() => setTonight(true)} data-testid="media-tonight">{t("deck.media.tonight")}</button>
				</div>
				<span className="grow" />
				<input ref={fileRef} type="file" accept="image/png,image/jpeg,image/webp" hidden onChange={(event) => void pick(event)} data-testid="media-upload-input" />
				<button type="button" className="btn btn-text" onClick={() => fileRef.current?.click()} disabled={upload.isPending}>{t("deck.media.upload")}</button>
				{canPack ? (
					<button type="button" className="btn btn-text" onClick={pack} disabled={!prompt.trim() || create.isPending} title={t("deck.media.adPackHint")} data-testid="media-ad-pack">
						{t("deck.media.adPack")}
					</button>
				) : null}
				<button type="button" className="btn btn-primary" onClick={submit} disabled={!capability || !words || create.isPending} data-testid="media-make">
					{t("deck.media.make")}
				</button>
			</div>
			{error ? <p className="small media-error" role="alert">{t(`deck.media.error.${error}`)}</p> : null}
		</div>
	);
}

/** One thing in the library: the picture, video or sound, its words, and what can be done with it. */
function MediaCard({ item, canUseAsInput, onUse }: { item: MediaItem; canUseAsInput: boolean; onUse: (item: MediaItem) => void }) {
	const { t } = useTranslation();
	const remove = useRemoveMedia();
	const [armed, setArmed] = useState(false);
	const file = item.files[0];
	useEffect(() => {
		if (!armed) return;
		const timer = window.setTimeout(() => setArmed(false), 4_000);
		return () => window.clearTimeout(timer);
	}, [armed]);
	const src = file ? mediaUrl(item.id, file.name) : "";
	return (
		<article className={`media-card ${item.state}`} data-testid={`media-item-${item.id}`} data-state={item.state}>
			<div className="media-frame">
				{item.state === "done" && file ? (
					file.type.startsWith("video/") ? (
						<video src={src} controls muted loop playsInline preload="metadata" />
					) : file.type.startsWith("audio/") ? (
						<div className="media-sound">
							<span className="media-wave" aria-hidden="true">{Array.from({ length: 24 }, (_, index) => <i key={index} style={{ height: `${20 + ((index * 37) % 70)}%` }} />)}</span>
							<audio src={src} controls preload="metadata" />
						</div>
					) : (
						<img src={src} alt={item.prompt} loading="lazy" />
					)
				) : (
					<span className="media-state small">
						{item.state === "failed"
							? t(`deck.media.failure.${item.failure ?? "failed"}`)
							: item.state === "scheduled"
								? t("deck.media.state.scheduled", { time: item.runAt ? new Date(item.runAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "" })
								: t(`deck.media.state.${item.state}`)}
					</span>
				)}
			</div>
			<p className="media-words small" dir="auto" title={item.prompt}>{item.prompt}</p>
			<p className="media-meta small t-2">
				<span>{t(`deck.media.kindShort.${item.kind}`)}</span>
				{item.source === "x00" ? <span>X-00</span> : null}
				<span className="grow" />
				{canUseAsInput && item.kind === "image" && item.state === "done" ? (
					<button type="button" className="btn-text media-act" onClick={() => onUse(item)}>{t("deck.media.use")}</button>
				) : null}
				{item.state === "done" && file ? (
					<a className="btn-text media-act" href={src} download={`x-robot-${item.id.slice(0, 8)}.${file.name.split(".").pop()}`}>{t("deck.media.save")}</a>
				) : null}
				<button
					type="button"
					className={armed ? "btn-text media-act armed" : "btn-text media-act"}
					onClick={() => (armed ? remove.mutate(item.id) : setArmed(true))}
					data-testid={`media-delete-${item.id}`}
				>
					{armed ? t("deck.media.confirmDelete") : t("deck.media.delete")}
				</button>
			</p>
		</article>
	);
}

/**
 * The media library on Home: what the person's GPU server made for them and
 * for X-00, with a composer to ask for more. Voice never lands here: it is
 * live and never kept.
 */
export function DeckMedia() {
	const { t } = useTranslation();
	const info = useMediaInfo();
	const items = useMediaItems();
	const [input, setInput] = useState<MediaItem | null>(null);
	const capabilities = info.data?.capabilities ?? [];
	const canUseAsInput = info.data?.enabled === true && capabilities.some((capability) => capability.input === "image");
	const list = items.data ?? [];
	return (
		<section className="sec" id="deck-media" data-testid="deck-media">
			<h2 className="d1">{t("deck.media.title")}</h2>
			<p className="lead-line">{t("deck.media.lead")}</p>
			<ServerLine />
			{info.data?.enabled ? <Composer capabilities={capabilities} input={input} onClearInput={() => setInput(null)} /> : null}
			{list.length === 0 ? (
				<p className="small t-2">{t("deck.media.empty")}</p>
			) : (
				<div className="media-grid">
					{list.map((item) => (
						<MediaCard key={item.id} item={item} canUseAsInput={canUseAsInput} onUse={setInput} />
					))}
				</div>
			)}
		</section>
	);
}
