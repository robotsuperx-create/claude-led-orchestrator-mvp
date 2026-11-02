import { describe, expect, it, vi } from "vitest";
import { editContextMenuTemplate, type EditableContents } from "./edit-context-menu";

const allFlags = { canUndo: true, canRedo: true, canCut: true, canCopy: true, canPaste: true, canSelectAll: true, canDelete: true, canEditRichly: false };

function contents(): EditableContents & Record<string, ReturnType<typeof vi.fn>> {
	return { undo: vi.fn(), redo: vi.fn(), cut: vi.fn(), copy: vi.fn(), paste: vi.fn(), selectAll: vi.fn() };
}

describe("editContextMenuTemplate", () => {
	it("offers paste in a text field and pastes into that page", () => {
		const target = contents();
		const template = editContextMenuTemplate({ isEditable: true, selectionText: "", editFlags: allFlags }, target, "en");
		const labels = template?.filter((item) => item.type !== "separator").map((item) => item.label);
		expect(labels).toEqual(["Undo", "Redo", "Cut", "Copy", "Paste", "Select All"]);
		const paste = template?.find((item) => item.label === "Paste");
		paste?.click?.({} as never, undefined, {} as never);
		expect(target.paste).toHaveBeenCalledOnce();
	});

	it("speaks the app's language", () => {
		const template = editContextMenuTemplate({ isEditable: true, selectionText: "", editFlags: allFlags }, contents(), "ar");
		expect(template?.map((item) => item.label)).toContain("لصق");
	});

	it("follows what the field allows", () => {
		const template = editContextMenuTemplate({ isEditable: true, selectionText: "", editFlags: { ...allFlags, canPaste: false, canCut: false } }, contents(), "en");
		expect(template?.find((item) => item.label === "Paste")?.enabled).toBe(false);
		expect(template?.find((item) => item.label === "Cut")?.enabled).toBe(false);
	});

	it("offers only copy for selected text outside a field", () => {
		const target = contents();
		const template = editContextMenuTemplate({ isEditable: false, selectionText: "a branch name", editFlags: allFlags }, target, "en");
		expect(template?.map((item) => item.label)).toEqual(["Copy"]);
		template?.[0]?.click?.({} as never, undefined, {} as never);
		expect(target.copy).toHaveBeenCalledOnce();
	});

	it("stays out of the way elsewhere", () => {
		expect(editContextMenuTemplate({ isEditable: false, selectionText: "  ", editFlags: allFlags }, contents(), "en")).toBeNull();
	});
});
