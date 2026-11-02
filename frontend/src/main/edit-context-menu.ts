import type { ContextMenuParams, MenuItemConstructorOptions } from "electron";
import { catalogFor, type MessageKey } from "../renderer/i18n/messages";
import type { AppLocale } from "../shared/ui-locale";

// Electron shows no context menu of its own, so without this a right-click in a
// text field offers no way to paste (an API key, a path, a task). This menu
// covers text fields and selected text in the app's own UI. Surfaces with their
// own menu (the terminal, the sidebar) prevent the DOM event, and Electron then
// never raises "context-menu", so they keep theirs.

/** The editing calls the menu makes; WebContents satisfies it. */
export type EditableContents = {
	undo(): void;
	redo(): void;
	cut(): void;
	copy(): void;
	paste(): void;
	selectAll(): void;
};

type MenuParams = Pick<ContextMenuParams, "isEditable" | "selectionText" | "editFlags">;

/**
 * The right-click menu for a text field or a selection, or null when the
 * click landed on neither. Labels follow the app's language.
 */
export function editContextMenuTemplate(params: MenuParams, contents: EditableContents, locale: AppLocale): MenuItemConstructorOptions[] | null {
	const hasSelection = params.selectionText.trim().length > 0;
	if (!params.isEditable && !hasSelection) return null;
	const catalog = catalogFor(locale);
	const label = (key: MessageKey) => catalog[key] ?? key;
	const flags = params.editFlags;
	if (!params.isEditable) {
		return [{ label: label("titlebar.copy"), enabled: flags.canCopy, click: () => contents.copy() }];
	}
	return [
		{ label: label("titlebar.undo"), enabled: flags.canUndo, click: () => contents.undo() },
		{ label: label("titlebar.redo"), enabled: flags.canRedo, click: () => contents.redo() },
		{ type: "separator" },
		{ label: label("titlebar.cut"), enabled: flags.canCut, click: () => contents.cut() },
		{ label: label("titlebar.copy"), enabled: flags.canCopy, click: () => contents.copy() },
		{ label: label("titlebar.paste"), enabled: flags.canPaste, click: () => contents.paste() },
		{ type: "separator" },
		{ label: label("titlebar.selectAll"), enabled: flags.canSelectAll, click: () => contents.selectAll() },
	];
}
