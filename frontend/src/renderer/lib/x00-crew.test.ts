import { describe, expect, it } from "vitest";
import { CREW_SIZE, crewMember } from "./x00-crew";

describe("crewMember", () => {
	it("gives the first step X-01 and the sixteenth X-16", () => {
		expect(crewMember(0).callSign).toBe("X-01");
		expect(crewMember(15).callSign).toBe("X-16");
	});

	it("repeats the crew after X-16", () => {
		expect(crewMember(CREW_SIZE).callSign).toBe("X-01");
		expect(crewMember(CREW_SIZE + 2).callSign).toBe("X-03");
	});

	it("has a portrait for every crew member", () => {
		for (let index = 0; index < CREW_SIZE; index += 1) expect(crewMember(index).portrait).toBeTruthy();
	});

	it("never breaks on an odd index", () => {
		expect(crewMember(-1).callSign).toBe("X-16");
		expect(crewMember(2.7).callSign).toBe("X-03");
	});
});
