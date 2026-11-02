import { describe, expect, it, vi } from "vitest";
import { canTryWebGL, perspective, rotationXY, X00PortraitEngine } from "./x00-portrait";

describe("X00PortraitEngine", () => {
	it("falls back to the still image where WebGL cannot run", () => {
		const host = document.createElement("div");
		const onFallback = vi.fn();
		const engine = new X00PortraitEngine(host, { art: { map: "map.webp", data: "data.webp" }, state: "watching", onFallback });
		expect(canTryWebGL()).toBe(false);
		expect(onFallback).toHaveBeenCalledOnce();
		expect(onFallback).toHaveBeenCalledWith("no webgl");
		expect(host.querySelector("canvas")).toBeNull();
		engine.setState("working");
		engine.dispose();
		engine.dispose();
	});
});

describe("portrait matrices", () => {
	it("builds an identity rotation at rest", () => {
		expect(Array.from(rotationXY(0, 0), (value) => value + 0)).toEqual([1, 0, 0, 0, 1, 0, 0, 0, 1]);
	});

	it("keeps rotations orthonormal", () => {
		const r = rotationXY(0.3, -0.2);
		const column = (i: number) => [r[i * 3], r[i * 3 + 1], r[i * 3 + 2]];
		const dot = (a: number[], b: number[]) => a[0] * b[0] + a[1] * b[1] + a[2] * b[2];
		for (let i = 0; i < 3; i += 1) {
			expect(dot(column(i), column(i))).toBeCloseTo(1, 6);
			for (let j = i + 1; j < 3; j += 1) expect(dot(column(i), column(j))).toBeCloseTo(0, 6);
		}
	});

	it("projects with the camera looking down -z", () => {
		const p = perspective(Math.PI / 2, 1, 0.1, 20);
		expect(p[0]).toBeCloseTo(1, 6);
		expect(p[5]).toBeCloseTo(1, 6);
		expect(p[11]).toBe(-1);
	});
});
