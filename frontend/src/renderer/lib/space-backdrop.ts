// Deep space behind X-ROBOT-AI: solemn black with bright blue stars. Three
// depths of stars twinkle, drift slowly and lean away from the pointer; the
// nearest burn with blue halos and a few carry diffraction spikes; a faint
// blue haze gives the black some depth; now and then a shooting star crosses.
// A 2D canvas at ~30 fps. It pauses when the window is hidden or the canvas is
// off screen, and draws one still frame under reduced motion.

export type StarDepth = 0 | 1 | 2;
export type Star = { x: number; y: number; depth: StarDepth; size: number; color: number; phase: number; speed: number; hero: boolean };
type Meteor = { x: number; y: number; vx: number; vy: number; life: number; age: number };

// Mostly blue: ice, sky and electric blue, with a few white-hot stars.
export const STAR_PALETTE = ["#8fd0ff", "#6fb8ff", "#a9dcff", "#c6e3f0", "#4f9dff", "#ffffff", "#7ec8ff", "#3d8bff"] as const;
const FRAME_MS = 1000 / 30;
const SPRITE = 64;

function random(seed: number) {
	let state = seed % 2147483647;
	if (state <= 0) state += 2147483646;
	return () => {
		state = (state * 16807) % 2147483647;
		return (state - 1) / 2147483646;
	};
}

/** The starfield for a box of this size in CSS pixels (deterministic for a seed). */
export function makeStars(width: number, height: number, seed = 20701): Star[] {
	const next = random(seed);
	const count = Math.max(160, Math.min(1400, Math.round((width * height) / 1500)));
	const stars: Star[] = [];
	for (let i = 0; i < count; i += 1) {
		const roll = next();
		const depth: StarDepth = roll < 0.66 ? 0 : roll < 0.9 ? 1 : 2;
		const hero = depth === 2 && next() < 0.16;
		const base = depth === 0 ? 0.55 : depth === 1 ? 0.95 : 1.45;
		stars.push({
			x: next(),
			y: next(),
			depth,
			size: base * (0.75 + next() * 0.6) * (hero ? 1.5 : 1),
			color: Math.floor(next() * STAR_PALETTE.length),
			phase: next() * Math.PI * 2,
			speed: 0.35 + next() * 1.9,
			hero,
		});
	}
	return stars;
}

export function prefersReducedMotion(): boolean {
	return typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

function hexToRgb(hex: string): [number, number, number] {
	const value = Number.parseInt(hex.slice(1), 16);
	return [(value >> 16) & 255, (value >> 8) & 255, value & 255];
}

/** A real 2D context (test doubles and broken drivers hand back stubs). */
function drawable(context: CanvasRenderingContext2D | null | undefined): context is CanvasRenderingContext2D {
	return !!context && typeof context.createRadialGradient === "function" && typeof context.drawImage === "function" && typeof context.clearRect === "function";
}

function makeCanvas(width: number, height: number): HTMLCanvasElement | null {
	if (typeof document === "undefined") return null;
	const canvas = document.createElement("canvas");
	canvas.width = Math.max(1, width);
	canvas.height = Math.max(1, height);
	return canvas;
}

/** One soft glow per palette colour: a white-hot core fading through blue. */
function makeSprites(): HTMLCanvasElement[] {
	const sprites: HTMLCanvasElement[] = [];
	for (const hex of STAR_PALETTE) {
		const canvas = makeCanvas(SPRITE, SPRITE);
		const ctx = canvas?.getContext("2d");
		if (!canvas || !drawable(ctx)) return [];
		const [r, g, b] = hexToRgb(hex);
		const half = SPRITE / 2;
		const glow = ctx.createRadialGradient(half, half, 0, half, half, half);
		glow.addColorStop(0, "rgba(255, 255, 255, 1)");
		glow.addColorStop(0.08, `rgba(${r}, ${g}, ${b}, 1)`);
		glow.addColorStop(0.22, `rgba(${r}, ${g}, ${b}, 0.42)`);
		glow.addColorStop(0.5, `rgba(${r}, ${g}, ${b}, 0.1)`);
		glow.addColorStop(1, `rgba(${r}, ${g}, ${b}, 0)`);
		ctx.fillStyle = glow;
		ctx.fillRect(0, 0, SPRITE, SPRITE);
		sprites.push(canvas);
	}
	return sprites;
}

/** A faint blue haze and a dusty band, so the black has depth but stays black. */
function makeNebula(width: number, height: number, seed: number): HTMLCanvasElement | null {
	const canvas = makeCanvas(width, height);
	const ctx = canvas?.getContext("2d");
	if (!canvas || !drawable(ctx)) return null;
	const next = random(seed);
	const clouds: Array<[number, number, number, string]> = [
		[0.78, 0.18, 0.55, "rgba(26, 72, 160, 0.16)"],
		[0.16, 0.82, 0.5, "rgba(18, 52, 128, 0.14)"],
		[0.5, 0.48, 0.38, "rgba(40, 110, 220, 0.06)"],
	];
	const span = Math.max(width, height);
	for (const [cx, cy, radius, color] of clouds) {
		const x = cx * width;
		const y = cy * height;
		const gradient = ctx.createRadialGradient(x, y, 0, x, y, radius * span);
		gradient.addColorStop(0, color);
		gradient.addColorStop(1, "rgba(0, 0, 0, 0)");
		ctx.fillStyle = gradient;
		ctx.fillRect(0, 0, width, height);
	}
	// The band: soft blue dust along a diagonal, like a far arm of the galaxy.
	ctx.save();
	ctx.translate(width / 2, height / 2);
	ctx.rotate(-0.42);
	const bandHeight = height * 0.34;
	const band = ctx.createLinearGradient(0, -bandHeight / 2, 0, bandHeight / 2);
	band.addColorStop(0, "rgba(60, 130, 255, 0)");
	band.addColorStop(0.5, "rgba(70, 140, 255, 0.07)");
	band.addColorStop(1, "rgba(60, 130, 255, 0)");
	ctx.fillStyle = band;
	ctx.fillRect(-span, -bandHeight / 2, span * 2, bandHeight);
	const dust = Math.round((width * height) / 900);
	for (let i = 0; i < dust; i += 1) {
		const along = (next() - 0.5) * span * 1.6;
		const across = (next() + next() + next() - 1.5) * bandHeight * 0.32;
		ctx.fillStyle = `rgba(150, 200, 255, ${0.05 + next() * 0.12})`;
		ctx.fillRect(along, across, 1, 1);
	}
	ctx.restore();
	return canvas;
}

/** Draws the starfield into `canvas`, sized to its own box. Call dispose() to stop. */
export class SpaceBackdropEngine {
	private readonly context: CanvasRenderingContext2D | null;
	private readonly reduced = prefersReducedMotion();
	private readonly sprites: HTMLCanvasElement[];
	private nebula: HTMLCanvasElement | null = null;
	private stars: Star[] = [];
	private meteor: Meteor | null = null;
	private nextMeteor = 0;
	private raf = 0;
	private last = 0;
	private t = 0;
	private px = 0;
	private py = 0;
	private tx = 0;
	private ty = 0;
	private width = 0;
	private height = 0;
	private ratio = 1;
	private visible = true;
	private disposed = false;
	private cleanups: Array<() => void> = [];

	constructor(private readonly canvas: HTMLCanvasElement) {
		let context: CanvasRenderingContext2D | null = null;
		try {
			context = canvas.getContext("2d", { alpha: true });
		} catch {
			context = null;
		}
		this.context = drawable(context) ? context : null;
		this.sprites = this.context ? makeSprites() : [];
		if (!this.context) return;
		this.layout();
		const onPointer = (event: PointerEvent) => {
			this.tx = event.clientX / Math.max(1, window.innerWidth) - 0.5;
			this.ty = event.clientY / Math.max(1, window.innerHeight) - 0.5;
		};
		const onVisibility = () => this.sync();
		window.addEventListener("pointermove", onPointer, { passive: true });
		document.addEventListener("visibilitychange", onVisibility);
		this.cleanups.push(() => window.removeEventListener("pointermove", onPointer));
		this.cleanups.push(() => document.removeEventListener("visibilitychange", onVisibility));
		if (typeof ResizeObserver !== "undefined") {
			const resize = new ResizeObserver(() => {
				this.layout();
				if (this.reduced) this.draw(0);
			});
			resize.observe(canvas);
			this.cleanups.push(() => resize.disconnect());
		}
		if (typeof IntersectionObserver !== "undefined") {
			const intersect = new IntersectionObserver(([entry]) => {
				this.visible = entry?.isIntersecting ?? true;
				this.sync();
			});
			intersect.observe(canvas);
			this.cleanups.push(() => intersect.disconnect());
		}
		this.nextMeteor = 5 + Math.random() * 8;
		if (this.reduced) this.draw(0);
		else this.sync();
	}

	dispose(): void {
		this.disposed = true;
		this.stop();
		for (const cleanup of this.cleanups) cleanup();
		this.cleanups = [];
	}

	private layout(): void {
		const rect = this.canvas.getBoundingClientRect();
		this.ratio = Math.min(window.devicePixelRatio || 1, 1.5);
		const width = Math.max(1, Math.round(rect.width * this.ratio));
		const height = Math.max(1, Math.round(rect.height * this.ratio));
		if (width === this.width && height === this.height) return;
		this.width = width;
		this.height = height;
		this.canvas.width = width;
		this.canvas.height = height;
		this.stars = makeStars(rect.width, rect.height);
		// The haze is soft, so a half-size canvas scaled up is indistinguishable.
		this.nebula = makeNebula(Math.ceil(width / 2), Math.ceil(height / 2), 4401);
	}

	private sync(): void {
		const on = !this.disposed && !this.reduced && this.visible && !document.hidden;
		if (on && !this.raf) {
			this.last = performance.now();
			const tick = (now: number) => {
				this.raf = window.requestAnimationFrame(tick);
				if (now - this.last < FRAME_MS) return;
				const dt = Math.min(0.1, (now - this.last) / 1000);
				this.last = now;
				this.t += dt;
				this.draw(dt);
			};
			this.raf = window.requestAnimationFrame(tick);
		} else if (!on) {
			this.stop();
		}
	}

	private stop(): void {
		if (this.raf) window.cancelAnimationFrame(this.raf);
		this.raf = 0;
	}

	private draw(dt: number): void {
		const ctx = this.context;
		if (!ctx || this.width < 2 || this.height < 2) return;
		const { width: w, height: h, ratio } = this;
		ctx.clearRect(0, 0, w, h);
		this.px += (this.tx - this.px) * Math.min(1, dt * 1.5);
		this.py += (this.ty - this.py) * Math.min(1, dt * 1.5);
		if (this.nebula) {
			// The haze sits farthest away: it barely leans.
			const lean = 4 * ratio;
			ctx.globalAlpha = 1;
			ctx.drawImage(this.nebula, -lean - this.px * lean, -lean - this.py * lean, w + lean * 2, h + lean * 2);
		}
		ctx.globalCompositeOperation = "lighter";
		for (const star of this.stars) {
			const drift = (star.depth + 1) * 0.0018 * this.t;
			const lean = (star.depth + 1) * 7 * ratio;
			let x = ((star.x + drift) % 1) * w - this.px * lean;
			const y = star.y * h - this.py * lean;
			if (x < -8) x += w;
			const wave = Math.sin(this.t * star.speed + star.phase);
			const radius = star.size * ratio;
			if (star.depth === 0) {
				// Far stars: crisp pin-points that shimmer.
				ctx.globalAlpha = this.reduced ? 0.7 : 0.5 + 0.35 * wave;
				ctx.fillStyle = STAR_PALETTE[star.color];
				const side = Math.max(ratio, radius * 1.6);
				ctx.fillRect(x - side / 2, y - side / 2, side, side);
				continue;
			}
			const twinkle = this.reduced ? 0.9 : 0.68 + 0.32 * wave;
			const sprite = this.sprites[star.color];
			if (sprite) {
				const glow = radius * (star.depth === 2 ? 10 : 7);
				ctx.globalAlpha = twinkle;
				ctx.drawImage(sprite, x - glow / 2, y - glow / 2, glow, glow);
			} else {
				ctx.globalAlpha = twinkle;
				ctx.fillStyle = STAR_PALETTE[star.color];
				ctx.beginPath();
				ctx.arc(x, y, radius, 0, Math.PI * 2);
				ctx.fill();
			}
			if (star.hero) {
				// Diffraction spikes: the brightest stars look like a telescope sees them.
				const reach = radius * (9 + 3 * wave);
				const spike = ctx.createLinearGradient(x - reach, y, x + reach, y);
				spike.addColorStop(0, "rgba(111, 184, 255, 0)");
				spike.addColorStop(0.5, "rgba(198, 227, 255, 0.9)");
				spike.addColorStop(1, "rgba(111, 184, 255, 0)");
				ctx.globalAlpha = 0.55 * twinkle;
				ctx.fillStyle = spike;
				ctx.fillRect(x - reach, y - ratio * 0.45, reach * 2, ratio * 0.9);
				const vertical = ctx.createLinearGradient(x, y - reach, x, y + reach);
				vertical.addColorStop(0, "rgba(111, 184, 255, 0)");
				vertical.addColorStop(0.5, "rgba(198, 227, 255, 0.9)");
				vertical.addColorStop(1, "rgba(111, 184, 255, 0)");
				ctx.fillStyle = vertical;
				ctx.fillRect(x - ratio * 0.45, y - reach, ratio * 0.9, reach * 2);
			}
		}
		if (!this.reduced) this.drawMeteor(ctx, dt);
		ctx.globalAlpha = 1;
		ctx.globalCompositeOperation = "source-over";
	}

	private drawMeteor(ctx: CanvasRenderingContext2D, dt: number): void {
		const { width: w, height: h, ratio } = this;
		if (!this.meteor) {
			this.nextMeteor -= dt;
			if (this.nextMeteor > 0) return;
			this.nextMeteor = 10 + Math.random() * 14;
			const fromLeft = Math.random() < 0.5;
			this.meteor = {
				x: fromLeft ? Math.random() * w * 0.5 : w * (0.5 + Math.random() * 0.5),
				y: Math.random() * h * 0.35,
				vx: (fromLeft ? 1 : -1) * w * (0.35 + Math.random() * 0.2),
				vy: h * (0.12 + Math.random() * 0.1),
				life: 1.1,
				age: 0,
			};
		}
		const meteor = this.meteor;
		meteor.age += dt;
		meteor.x += meteor.vx * dt;
		meteor.y += meteor.vy * dt;
		const fade = Math.sin(Math.min(1, meteor.age / meteor.life) * Math.PI);
		const tail = 0.18;
		const gradient = ctx.createLinearGradient(meteor.x, meteor.y, meteor.x - meteor.vx * tail, meteor.y - meteor.vy * tail);
		gradient.addColorStop(0, "rgba(220, 240, 255, 1)");
		gradient.addColorStop(0.3, "rgba(111, 184, 255, 0.6)");
		gradient.addColorStop(1, "rgba(61, 139, 255, 0)");
		ctx.globalAlpha = fade;
		ctx.strokeStyle = gradient;
		ctx.lineWidth = 1.6 * ratio;
		ctx.lineCap = "round";
		ctx.beginPath();
		ctx.moveTo(meteor.x, meteor.y);
		ctx.lineTo(meteor.x - meteor.vx * tail, meteor.y - meteor.vy * tail);
		ctx.stroke();
		const head = this.sprites[2];
		if (head) {
			const size = 14 * ratio;
			ctx.drawImage(head, meteor.x - size / 2, meteor.y - size / 2, size, size);
		}
		if (meteor.age >= meteor.life) this.meteor = null;
	}
}
