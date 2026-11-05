import { useEffect, useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { RotateCw, Store } from "lucide-react";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { topbarProjectLabelClass } from "./TopbarButton";
import { hidesShellTopbar } from "../lib/platform";
import {
	parseStoresHubUrl,
	probeStoresHub,
	readStoredStoresHubUrl,
	writeStoredStoresHubUrl,
} from "../lib/stores-hub";
import { cn } from "../lib/utils";

const STORES_FOLDER = "x-robot-stores";
const STORES_START_COMMAND = "npm run preview:hub";

// Scripts and forms let the storefronts work; same-origin keeps their own
// storage (carts) working and is safe because the hub is never the app's
// origin (parseStoresHubUrl). Popups are allowed so target="_blank" links
// (WhatsApp, social) reach the shell's window-open handler, which denies the
// in-app window and opens http(s) links in the system browser.
const FRAME_SANDBOX = "allow-scripts allow-same-origin allow-forms allow-popups";

type HubStatus = "checking" | "reachable" | "unreachable";

export function StoresView() {
	const { t } = useTranslation();
	const showPageTitle = hidesShellTopbar();
	const addressErrorId = useId();
	const [hubUrl, setHubUrl] = useState(readStoredStoresHubUrl);
	const [draft, setDraft] = useState(hubUrl);
	const [addressError, setAddressError] = useState<"invalid" | "notLocal" | null>(null);
	const [attempt, setAttempt] = useState(0);
	const [status, setStatus] = useState<HubStatus>("checking");

	useEffect(() => {
		const controller = new AbortController();
		setStatus("checking");
		void probeStoresHub(hubUrl, controller.signal).then((reachable) => {
			if (!controller.signal.aborted) setStatus(reachable ? "reachable" : "unreachable");
		});
		return () => controller.abort();
	}, [hubUrl, attempt]);

	const open = (event: FormEvent<HTMLFormElement>) => {
		event.preventDefault();
		const parsed = parseStoresHubUrl(draft);
		if (!parsed.ok) {
			setAddressError(parsed.reason);
			return;
		}
		setAddressError(null);
		setDraft(parsed.url);
		writeStoredStoresHubUrl(parsed.url);
		setHubUrl(parsed.url);
		setAttempt((value) => value + 1);
	};
	const reload = () => setAttempt((value) => value + 1);

	return (
		<div className="flex min-h-0 flex-1 flex-col bg-background">
			<header className="workspace-topbar-container center-panel-titlebar flex h-toolbar shrink-0 items-center justify-between gap-3 border-b border-border-strong pe-2">
				<div className="flex min-w-0 items-center gap-2">
					<h1 className={showPageTitle ? cn(topbarProjectLabelClass, "inline-flex items-center gap-1.5") : "sr-only"}>
						{showPageTitle ? <Store className="size-icon-md" aria-hidden="true" /> : null}
						{t("stores.title")}
					</h1>
					{!showPageTitle ? <p className="truncate text-xs text-muted-foreground">{t("stores.description")}</p> : null}
				</div>
				<form className="flex shrink-0 items-center gap-1" noValidate onSubmit={open}>
					<Input
						aria-describedby={addressError ? addressErrorId : undefined}
						aria-invalid={addressError ? true : undefined}
						aria-label={t("stores.address.label")}
						autoComplete="off"
						className="h-control-md w-60 font-mono text-xs"
						dir="ltr"
						inputMode="url"
						onChange={(event) => {
							setDraft(event.target.value);
							if (addressError) setAddressError(null);
						}}
						spellCheck={false}
						value={draft}
					/>
					<Button size="sm" type="submit" variant="secondary">
						{t("stores.open")}
					</Button>
					<Button size="sm" type="button" variant="ghost" onClick={reload}>
						<RotateCw className="size-icon-md" aria-hidden="true" />
						{t("stores.reload")}
					</Button>
				</form>
			</header>
			{addressError ? (
				<p id={addressErrorId} role="alert" className="shrink-0 px-4 py-1.5 text-end text-xs text-destructive">
					{t(addressError === "notLocal" ? "stores.address.notLocal" : "stores.address.invalid")}
				</p>
			) : null}
			<main className="relative min-h-0 flex-1 overflow-hidden">
				{status === "reachable" ? (
					<iframe
						key={attempt}
						// Web pages assume the browser's white canvas; keep that while they load.
						className="absolute inset-0 h-full w-full border-0 bg-white"
						referrerPolicy="no-referrer"
						sandbox={FRAME_SANDBOX}
						src={hubUrl}
						title={t("stores.frameTitle")}
					/>
				) : status === "checking" ? (
					<p role="status" className="px-4 py-10 text-center text-sm text-muted-foreground">
						{t("stores.checking")}
					</p>
				) : (
					<StoresHubUnreachable onRetry={reload} />
				)}
			</main>
		</div>
	);
}

function StoresHubUnreachable({ onRetry }: { onRetry: () => void }) {
	const { t } = useTranslation();
	return (
		<div className="flex h-full min-h-0 items-center justify-center overflow-y-auto px-6" data-testid="stores-unreachable">
			<div className="flex w-full max-w-preview-content flex-col items-center pb-empty-offset-y text-center">
				<Store className="mb-3 size-icon-xl text-muted-foreground" aria-hidden="true" />
				<h2 className="text-heading-sm font-semibold tracking-tight text-foreground">{t("stores.unreachable.title")}</h2>
				<p className="mt-2 text-md-sm leading-relaxed text-muted-foreground">
					{t("stores.unreachable.body", { folder: STORES_FOLDER })}
				</p>
				<code dir="ltr" className="mt-3 select-all rounded-md bg-muted px-2.5 py-1 font-mono text-xs text-foreground">
					{STORES_START_COMMAND}
				</code>
				<Button className="mt-4" onClick={onRetry}>
					{t("stores.unreachable.retry")}
				</Button>
			</div>
		</div>
	);
}
