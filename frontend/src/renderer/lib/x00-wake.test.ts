import { describe, expect, it } from "vitest";
import { wakeCommand } from "./x00-wake";

describe("X-00's wake word", () => {
	it("hears his name however the engine wrote it, and keeps the command", () => {
		expect(wakeCommand("X-00, how is the fleet?")).toBe("how is the fleet?");
		expect(wakeCommand("x zero make me a picture of a car")).toBe("make me a picture of a car");
		expect(wakeCommand("Hey X00. Start a run")).toBe("Start a run");
		expect(wakeCommand("Ex double zero")).toBe("");
		expect(wakeCommand("إكس زيرو، كيف حال الأسطول؟")).toBe("كيف حال الأسطول؟");
		expect(wakeCommand("يا اكس صفر صفر اصنع صورة")).toBe("اصنع صورة");
	});

	it("ignores lines that are not for him", () => {
		for (const line of ["the next zero day", "export 00 files", "X1000 is a model", "I said x is zero", "مرحبا كيف حالك", ""]) {
			expect(wakeCommand(line), line).toBeNull();
		}
	});
});
