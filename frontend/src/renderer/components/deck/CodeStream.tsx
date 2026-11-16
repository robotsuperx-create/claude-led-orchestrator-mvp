import { useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { useTranslation } from "react-i18next";
import type { ClaudeOrchestratorLiveFile } from "../../../shared/claude-orchestrator";

/** How many of the newest file's last lines the screen shows. */
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
export function CodeStream({ files, live }: { files: ClaudeOrchestratorLiveFile[]; live: boolean }) {
	const { t } = useTranslation();
	const [chosen, setChosen] = useState<string | null>(null);
	const current = files.find((file) => file.path === chosen) ?? files[0];
	const lines = useMemo(() => (current ? current.lines.slice(-SCREEN_LINES) : []), [current]);
	const firstNumber = current ? Math.max(1, current.added - lines.length + 1) : 1;
	const seen = useRef(new Set<string>());
	const [fresh, setFresh] = useState<Set<string>>(new Set());

	// Mark the lines this look brought, so only they type in.
	useEffect(() => {
		if (!current) return;
		const keys = lines.map((line, index) => lineKey(current.path, line, firstNumber + index));
		const arrived = new Set(keys.filter((key) => !seen.current.has(key)));
		for (const key of keys) seen.current.add(key);
		if (seen.current.size > 2_000) seen.current = new Set(keys);
		setFresh(arrived);
	}, [current, lines, firstNumber]);

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
				<div className="code-files" role="tablist" aria-label={t("deck.code.files")}>
					{files.map((file) => (
						<button
							key={file.path}
							type="button"
							role="tab"
							aria-selected={file.path === current?.path}
							className={file.path === current?.path ? "code-file on" : "code-file"}
							onClick={() => setChosen(file.path)}
						>
							<bdi className="code-file-path">{file.path}</bdi>
							<span className="code-file-count">+{file.added}</span>
							{file.removed > 0 ? <span className="code-file-count minus">−{file.removed}</span> : null}
						</button>
					))}
				</div>
			) : null}
			<div className="code-screen" dir="ltr">
				{current ? (
					<>
						<div className="code-path">
							<span>{current.path}</span>
							{current.created ? <span className="code-new">{t("deck.code.newFile")}</span> : null}
						</div>
						<pre className="code-lines" data-testid="deck-code-lines">
							{lines.map((line, index) => {
								const number = firstNumber + index;
								const key = lineKey(current.path, line, number);
								const isFresh = fresh.has(key);
								const order = isFresh ? [...fresh].indexOf(key) : 0;
								return (
									<span
										key={key}
										className={isFresh ? "code-line typing" : "code-line"}
										style={isFresh ? ({ "--d": `${Math.min(order, 12) * 90}ms`, "--n": Math.max(line.length, 1) } as CSSProperties) : undefined}
									>
										<span className="code-no" aria-hidden="true">{number}</span>
										<span className="code-text">{line || " "}</span>
									</span>
								);
							})}
							{live ? <span className="code-caret" aria-hidden="true" /> : null}
						</pre>
					</>
				) : (
					<p className="code-idle">{live ? t("deck.code.waiting") : t("deck.code.none")}</p>
				)}
			</div>
		</section>
	);
}
