import type { ReactNode } from "react";
import { Direction } from "radix-ui";
import { useTranslation } from "react-i18next";

/**
 * Gives Radix primitives the UI language's reading direction. Without it they
 * default to left-to-right and stamp `dir="ltr"` on their roots (scroll areas,
 * menus, selects), which would undo right-to-left layout in Arabic.
 */
export function AppDirection({ children }: { children: ReactNode }) {
	const { i18n } = useTranslation();
	return <Direction.DirectionProvider dir={i18n.dir(i18n.language)}>{children}</Direction.DirectionProvider>;
}
