import { useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import type { ClaudeOrchestratorLiveFile } from "../../../shared/claude-orchestrator";

/** How many rows the screen holds: file headers and their newest lines. */
const SCREEN_LINES = 18;

/** A stable signature of a file's last lines, to tell which lines are new since the last look. */
function lineKey(path: string, line: string, index: number): string {
	return `${path}\u0000${index}\u0000${line}`;
}

/**
 * The code stream under X-00: the crew's code as it lands, in phosphor green
 * on the void, newest file on the screen and the others on a strip above it.
 * Lines that arrived since the last look type themselves in; the caret
 * blinks while the run is live. Code is shown as text, never as markup, and
 * always reads left to right.
 */
type ScreenRow = { key: string; kind: "file" | "line"; text: string; number?: number; created?: boolean };

/** The screen's rows: one file when a tab is chosen, else the newest lines of every file, newest file first. */
function screenRows(files: ClaudeOrchestratorLiveFile[], chosen: ClaudeOrchestratorLiveFile | undefined): ScreenRow[] {
	const shown = chosen ? [chosen] : files;
	const rows: ScreenRow[] = [];
	for (const file of shown) {
		// A file needs room for its header and at least one line.
		if (SCREEN_LINES - rows.length < 2) break;
		const room = SCREEN_LINES - rows.length - 1;
		const lines = file.lines.slice(-Math.max(room, 1));
		const first = Math.max(1, file.added - lines.length + 1);
		rows.push({ key: `f\u0000${file.path}`, kind: "file", text: file.path, created: file.created });
		lines.forEach((line, index) => rows.push({ key: lineKey(file.path, line, first + index), kind: "line", text: line, number: first + index }));
	}
	return rows;
}

/**
 * The code stream under X-00: the crew's code as it lands, in phosphor green
 * on the void, newest file first, with a strip of every file to pick one.
 * Lines that arrived since the last look type themselves in; the caret
 * blinks while the run is live. Code is shown as text, never as markup, and
 * always reads left to right.
 */
export function CodeStream({ files, live }: { files: ClaudeOrchestratorLiveFile[]; live: boolean }) {
	const { t } = useTranslation();
	const [chosen, setChosen] = useState<string | null>(null);
	const picked = files.find((file) => file.path === chosen);
	const rows = useMemo(() => screenRows(files, picked), [files, picked]);
	const seen = useRef(new Set<string>());
	const [fresh, setFresh] = useState<string[]>([]);

	// Mark the lines this look brought, so only they type in.
	useEffect(() => {
		const keys = rows.filter((row) => row.kind === "line").map((row) => row.key);
		const arrived = keys.filter((key) => !seen.current.has(key));
		for (const key of keys) seen.current.add(key);
		if (seen.current.size > 2_000) seen.current = new Set(keys);
		setFresh(arrived);
	}, [rows]);

	const total = files.reduce((sum, file) => sum + file.added, 0);
	return (
		<section className="live-panel code-stream" aria-label={t("deck.code.title")} data-testid="deck-code-stream" data-live={live}>
			<header className="live-head">
				<span className={live ? "live-dot on" : "live-dot"} aria-hidden="true" />
				<h2 className="live-title">{t("deck.code.title")}</h2>
				<span className="live-meta small t-2">{t("deck.code.summary", { files: files.length, lines: total })}</span>
				{live ? <span className="live-badge micro">{t("deck.code.live")}</span> : null}
			</header>
			{files.length > 1 ? (
				<div className="code-files" role="tablist" aria-label={t("deck.code.files")} dir="ltr">
					{files.map((file) => (
						<button
							key={file.path}
							type="button"
							role="tab"
							aria-selected={file.path === picked?.path}
							className={file.path === picked?.path ? "code-file on" : "code-file"}
							onClick={() => setChosen((current) => (current === file.path ? null : file.path))}
						>
							<span className="code-file-path">{file.path}</span>
							<span className="code-file-count">+{file.added}</span>
							{file.removed > 0 ? <span className="code-file-count minus">−{file.removed}</span> : null}
						</button>
					))}
				</div>
			) : null}
			<div className="code-screen" dir="ltr">
				{rows.length > 0 ? (
					<pre className="code-lines" data-testid="deck-code-lines">
						{rows.map((row) => {
							if (row.kind === "file") {
								return (
									<span key={row.key} className="code-line code-path">
										<span className="code-no" aria-hidden="true">{"//"}</span>
										<span>{row.text}</span>
										{row.created ? <span className="code-new">{t("deck.code.newFile")}</span> : null}
									</span>
								);
							}
							const order = fresh.indexOf(row.key);
							const typing = order >= 0;
							return (
								<span
									key={row.key}
									className={typing ? "code-line typing" : "code-line"}
									style={typing ? ({ "--d": `${Math.min(order, 12) * 90}ms`, "--n": Math.max(row.text.length, 1) } as CSSProperties) : undefined}
								>
									<span className="code-no" aria-hidden="true">{row.number}</span>
									<span className="code-text">{row.text || " "}</span>
								</span>
							);
						})}
						{live ? <span className="code-caret" aria-hidden="true" /> : null}
					</pre>
				) : (
					<p className="code-idle">{live ? t("deck.code.waiting") : t("deck.code.none")}</p>
				)}
			</div>
		</section>
	);
}
