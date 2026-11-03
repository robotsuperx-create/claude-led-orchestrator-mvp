import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { useX00Store } from "../../stores/x00-store";
import { X00Mark } from "../X00Portrait";

/**
 * X-00's head in the titlebar, shown while the sidebar is hidden so he stays a
 * click away on every screen. It reads only his store (open, unseen alerts), so
 * the titlebar does not depend on the daemon or react-query.
 */
export function X00TitlebarButton({ className }: { className?: string }) {
	const { t } = useTranslation();
	const open = useX00Store((store) => store.open);
	const unseen = useX00Store((store) => store.unseen);
	const speaking = useX00Store((store) => store.speaking);
	const toggle = useX00Store((store) => store.toggle);
	const label = open ? t("x00.close") : t("x00.open");
	return (
		<button
			type="button"
			aria-label={label}
			aria-expanded={open}
			aria-controls="x00-panel"
			title={label}
			className={cn("x00-entry x00-titlebar-button relative", className)}
			data-speaking={speaking ? "true" : undefined}
			data-testid="x00-titlebar-button"
			onClick={toggle}
		>
			<span className="x00-entry-head">
				<X00Mark className="size-[18px]" />
			</span>
			{unseen > 0 ? (
				<span className="x00-entry-badge x00-titlebar-badge" data-testid="x00-titlebar-unseen">{unseen > 9 ? "9+" : unseen}</span>
			) : null}
		</button>
	);
}
