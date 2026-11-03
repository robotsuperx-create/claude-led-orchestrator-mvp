import { describe, expect, it } from "vitest";
import { STAR_PALETTE, makeStars } from "./space-backdrop";

describe("makeStars", () => {
	it("is the same sky every time for a seed", () => {
		expect(makeStars(1360, 860)).toEqual(makeStars(1360, 860));
		expect(makeStars(1360, 860, 7)).not.toEqual(makeStars(1360, 860));
	});

	it("fills a window with mostly far stars and a few bright near ones", () => {
		const stars = makeStars(1360, 860);
		expect(stars.length).toBeGreaterThan(600);
		const far = stars.filter((star) => star.depth === 0).length;
		const near = stars.filter((star) => star.depth === 2);
		expect(far / stars.length).toBeGreaterThan(0.55);
		expect(near.length).toBeGreaterThan(40);
		expect(near.some((star) => star.hero)).toBe(true);
		expect(stars.every((star) => star.hero === false || star.depth === 2)).toBe(true);
	});

	it("keeps every star inside the box and on the blue palette", () => {
		for (const star of makeStars(800, 600)) {
			expect(star.x).toBeGreaterThanOrEqual(0);
			expect(star.x).toBeLessThan(1);
			expect(star.y).toBeGreaterThanOrEqual(0);
			expect(star.y).toBeLessThan(1);
			expect(STAR_PALETTE[star.color]).toBeDefined();
		}
	});

	it("stays bounded on tiny and huge windows", () => {
		expect(makeStars(10, 10).length).toBe(160);
		expect(makeStars(7680, 4320).length).toBe(1400);
	});
});
