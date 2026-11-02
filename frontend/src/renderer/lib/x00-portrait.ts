// X-00, the commander: a relief portrait with its depth baked into a mesh, drawn
// in one WebGL pass with no post-processing. Desaturated chrome, lit only by
// the light in the room: ion while he watches, breathing while the crew works,
// the outcome's colour when a run lands, dark when a run fails. Ported from
// the X-ROBOT observation deck (dashboard/static/ui/portrait.js) without three.js.
//
// Budget: 60 fps on an integrated laptop GPU. If it cannot hold ~55 fps after
// the reveal, or WebGL is missing or lost, the still image takes over.

export type X00State = "watching" | "working" | "done" | "held" | "failed";

export const X00_STATES: readonly X00State[] = ["watching", "working", "done", "held", "failed"];

/** The light in the room for each state (linear-ish 0..1 RGB) and how much of it there is. */
const LIGHT_COLOUR: Record<X00State, [number, number, number]> = {
	watching: hex("#C6E3F0"),
	working: hex("#C6E3F0"),
	done: hex("#9FE3BD"),
	held: hex("#F0A58F"),
	failed: hex("#ECE9E2"),
};
const LIGHT_AMOUNT: Record<X00State, number> = { watching: 0.55, working: 0.55, done: 1, held: 1, failed: 0.12 };

function hex(value: string): [number, number, number] {
	const n = Number.parseInt(value.slice(1), 16);
	return [((n >> 16) & 255) / 255, ((n >> 8) & 255) / 255, (n & 255) / 255];
}

const SEGMENTS = 200;
const SLOW_FRAME = 1 / 52;
const SLOW_LIMIT = 40;

const VERT = `
attribute vec2 aPos; attribute vec2 aUv; attribute float aDepth; attribute vec2 aGrad;
uniform mat4 uProj; uniform mat4 uModelView; uniform mat3 uNormal;
uniform float uRelief; uniform float uReveal; uniform vec2 uHand; uniform float uBreath; uniform vec4 uHandBox;
varying vec2 vUv; varying vec3 vN; varying vec3 vView;
float box(vec2 uv, vec2 lo, vec2 hi, float soft){ vec2 a = smoothstep(lo - soft, lo + soft, uv), b = smoothstep(hi + soft, hi - soft, uv); return a.x * a.y * b.x * b.y; }
void main(){
  vUv = aUv; float k = uRelief * uReveal;
  vec3 p = vec3(aPos, aDepth * k);
  float chest = smoothstep(.2, .62, aUv.y) * smoothstep(.95, .7, aUv.y);
  p.y += chest * uBreath * .006;
  float hl = box(aUv, vec2(0., uHandBox.x), vec2(uHandBox.y, uHandBox.z), .06), hr = box(aUv, vec2(1. - uHandBox.y, uHandBox.x), vec2(1., uHandBox.z), .06);
  p.y += hl * uHand.x + hr * uHand.y;
  p.x += (hl * uHand.x - hr * uHand.y) * .25;
  vN = normalize(uNormal * normalize(vec3(-aGrad.x * k, -aGrad.y * k, 1.)));
  vec4 mv = uModelView * vec4(p, 1.); vView = normalize(-mv.xyz);
  gl_Position = uProj * mv;
}`;

// Shared uniforms must match the vertex shader's precision (highp), or the program will not link.
const FRAG = `
#ifdef GL_FRAGMENT_PRECISION_HIGH
precision highp float;
#else
precision mediump float;
#endif
uniform sampler2D uMap; uniform sampler2D uData;
uniform vec3 uLightCol; uniform vec3 uLightDir; uniform float uLight; uniform float uGlow; uniform float uReveal; uniform float uSat;
varying vec2 vUv; varying vec3 vN; varying vec3 vView;
void main(){
  vec4 c = texture2D(uMap, vUv); vec4 D = texture2D(uData, vUv);
  float a = c.a * smoothstep(0., .03, vUv.y) * smoothstep(0., .08, uReveal * 1.25 - (1. - vUv.y));
  if (a < .004) discard;
  vec3 col = pow(c.rgb, vec3(2.2)); float lum = dot(col, vec3(.2126, .7152, .0722));
  col = mix(col, vec3(lum), uSat);
  vec3 N = normalize(vN);
  float lam = clamp(dot(N, normalize(uLightDir)), 0., 1.);
  col *= (.22 + .78 * lam) * (.35 + .9 * uLight);
  col *= mix(vec3(1.), uLightCol, .3 * uLight);
  float ndv = clamp(abs(dot(N, normalize(vView))), 0., 1.);
  col += vec3(.93, .91, .89) * pow(max(1. - ndv, 0.), 3.) * .08;
  col = mix(col, uLightCol * (.3 + 1.1 * lum) * uGlow, D.g);
  gl_FragColor = vec4(pow(max(col, 0.), vec3(1. / 2.2)), a);
}`;

export function prefersReducedMotion(): boolean {
	return typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}

/** True when this environment can try WebGL at all (jsdom and old runtimes cannot). */
export function canTryWebGL(): boolean {
	return typeof window !== "undefined" && typeof window.WebGLRenderingContext !== "undefined";
}

type Art = { map: string; data: string };

type Options = {
	art: Art;
	state: X00State;
	/** Called once when the live portrait gives way to the still image. */
	onFallback: (reason: string) => void;
	/** Called once the relief is on screen. */
	onReady?: () => void;
};

/**
 * Mounts a canvas into `host` and drives the portrait. Call `setState` as the
 * room changes and `dispose` when the host goes away.
 */
export class X00PortraitEngine {
	private readonly host: HTMLElement;
	private readonly canvas: HTMLCanvasElement;
	private readonly reduced = prefersReducedMotion();
	private readonly onFallback: (reason: string) => void;
	private readonly onReady?: () => void;
	private gl: WebGLRenderingContext | null = null;
	private program: WebGLProgram | null = null;
	private buffers: WebGLBuffer[] = [];
	private textures: WebGLTexture[] = [];
	private uniforms: Record<string, WebGLUniformLocation | null> = {};
	private indexCount = 0;
	private disposed = false;
	private failed = false;
	private running = false;
	private raf = 0;
	private visible = true;
	private software = false;
	private loaded = false;
	private revealStart = 0;
	private last = 0;
	private frames = 0;
	private slow = 0;
	private t = 0;
	private rx = 0;
	private ry = 0;
	private mx = 0;
	private my = 0;
	private state: X00State;
	private lightCol: [number, number, number];
	private targetCol: [number, number, number];
	private light: number;
	private targetLight: number;
	private cleanups: Array<() => void> = [];

	constructor(host: HTMLElement, options: Options) {
		this.host = host;
		this.state = options.state;
		this.onFallback = options.onFallback;
		this.onReady = options.onReady;
		this.lightCol = [...LIGHT_COLOUR[this.state]];
		this.targetCol = [...LIGHT_COLOUR[this.state]];
		this.light = LIGHT_AMOUNT[this.state];
		this.targetLight = this.light;
		this.canvas = document.createElement("canvas");
		this.canvas.setAttribute("aria-hidden", "true");
		if (!canTryWebGL()) {
			this.fail("no webgl");
			return;
		}
		const attributes: WebGLContextAttributes = { antialias: false, alpha: true, premultipliedAlpha: false, depth: true, powerPreference: "default" };
		let gl: WebGLRenderingContext | null = null;
		try {
			gl = (this.canvas.getContext("webgl2", attributes) as WebGLRenderingContext | null) ?? this.canvas.getContext("webgl", attributes);
		} catch {
			gl = null;
		}
		if (!gl) {
			this.fail("no webgl");
			return;
		}
		this.gl = gl;
		try {
			this.setup(gl);
		} catch (error) {
			this.fail(error instanceof Error ? error.message : "setup");
			return;
		}
		host.appendChild(this.canvas);
		this.listen();
		this.layout();
		void this.load(options.art);
	}

	setState(state: X00State): void {
		if (this.state === state) return;
		this.state = state;
		this.targetCol = [...LIGHT_COLOUR[state]];
		this.targetLight = LIGHT_AMOUNT[state];
		if (this.reduced) this.draw(performance.now());
	}

	dispose(): void {
		if (this.disposed) return;
		this.disposed = true;
		this.stop();
		for (const cleanup of this.cleanups) cleanup();
		this.cleanups = [];
		const gl = this.gl;
		if (gl) {
			for (const buffer of this.buffers) gl.deleteBuffer(buffer);
			for (const texture of this.textures) gl.deleteTexture(texture);
			if (this.program) gl.deleteProgram(this.program);
			gl.getExtension("WEBGL_lose_context")?.loseContext();
		}
		this.gl = null;
		this.canvas.remove();
	}

	private fail(reason: string): void {
		if (this.failed) return;
		this.failed = true;
		this.stop();
		this.canvas.remove();
		this.onFallback(reason);
	}

	private setup(gl: WebGLRenderingContext): void {
		const compile = (type: number, source: string) => {
			const shader = gl.createShader(type);
			if (!shader) throw new Error("shader");
			gl.shaderSource(shader, source);
			gl.compileShader(shader);
			if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) throw new Error(`shader: ${gl.getShaderInfoLog(shader) ?? "compile"}`);
			return shader;
		};
		const program = gl.createProgram();
		if (!program) throw new Error("program");
		const vs = compile(gl.VERTEX_SHADER, VERT);
		const fs = compile(gl.FRAGMENT_SHADER, FRAG);
		gl.attachShader(program, vs);
		gl.attachShader(program, fs);
		gl.linkProgram(program);
		gl.deleteShader(vs);
		gl.deleteShader(fs);
		if (!gl.getProgramParameter(program, gl.LINK_STATUS)) throw new Error(`program: ${gl.getProgramInfoLog(program) ?? "link"}`);
		this.program = program;
		gl.useProgram(program);
		for (const name of ["uProj", "uModelView", "uNormal", "uRelief", "uReveal", "uHand", "uBreath", "uHandBox", "uMap", "uData", "uLightCol", "uLightDir", "uLight", "uGlow", "uSat"]) {
			this.uniforms[name] = gl.getUniformLocation(program, name);
		}
		gl.uniform1i(this.uniforms.uMap, 0);
		gl.uniform1i(this.uniforms.uData, 1);
		gl.uniform1f(this.uniforms.uRelief, 0.3);
		gl.uniform1f(this.uniforms.uSat, 0.9);
		gl.uniform4f(this.uniforms.uHandBox, 0.03, 0.33, 0.3, 0);
		gl.enable(gl.DEPTH_TEST);
		gl.enable(gl.BLEND);
		gl.blendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA);
		gl.clearColor(0, 0, 0, 0);
		const info = gl.getExtension("WEBGL_debug_renderer_info");
		const renderer = info ? String(gl.getParameter(info.UNMASKED_RENDERER_WEBGL) ?? "") : "";
		this.software = /swiftshader|llvmpipe|software/i.test(renderer);
	}

	private listen(): void {
		const onPointer = (event: PointerEvent) => {
			this.mx = event.clientX / Math.max(1, window.innerWidth) - 0.5;
			this.my = event.clientY / Math.max(1, window.innerHeight) - 0.5;
		};
		const onVisibility = () => {
			this.slow = 0;
			this.sync();
		};
		const onLost = (event: Event) => {
			event.preventDefault();
			this.fail("context lost");
		};
		window.addEventListener("pointermove", onPointer, { passive: true });
		document.addEventListener("visibilitychange", onVisibility);
		this.canvas.addEventListener("webglcontextlost", onLost);
		this.cleanups.push(() => window.removeEventListener("pointermove", onPointer));
		this.cleanups.push(() => document.removeEventListener("visibilitychange", onVisibility));
		this.cleanups.push(() => this.canvas.removeEventListener("webglcontextlost", onLost));
		if (typeof ResizeObserver !== "undefined") {
			const resize = new ResizeObserver(() => {
				this.layout();
				if (this.reduced) this.draw(performance.now());
			});
			resize.observe(this.host);
			this.cleanups.push(() => resize.disconnect());
		}
		if (typeof IntersectionObserver !== "undefined") {
			const intersect = new IntersectionObserver(([entry]) => {
				this.visible = entry?.isIntersecting ?? true;
				this.sync();
			});
			intersect.observe(this.host);
			this.cleanups.push(() => intersect.disconnect());
		}
	}

	private async load(art: Art): Promise<void> {
		const gl = this.gl;
		if (!gl) return;
		try {
			const [map, data] = await Promise.all([loadImage(art.map), loadImage(art.data)]);
			if (this.disposed || this.failed) return;
			this.textures.push(this.texture(gl, map, 0), this.texture(gl, data, 1));
			this.geometry(gl, data);
			this.loaded = true;
			this.revealStart = performance.now();
			this.last = this.revealStart;
			this.host.dataset.ready = "true";
			this.onReady?.();
			if (this.reduced) this.draw(this.revealStart);
			else this.sync();
		} catch {
			this.fail("art");
		}
	}

	private texture(gl: WebGLRenderingContext, image: HTMLImageElement, unit: number): WebGLTexture {
		const texture = gl.createTexture();
		if (!texture) throw new Error("texture");
		const webgl2 = typeof WebGL2RenderingContext !== "undefined" && gl instanceof WebGL2RenderingContext;
		gl.activeTexture(gl.TEXTURE0 + unit);
		gl.bindTexture(gl.TEXTURE_2D, texture);
		gl.pixelStorei(gl.UNPACK_FLIP_Y_WEBGL, true);
		gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, image);
		gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
		gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
		gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
		if (webgl2) {
			gl.generateMipmap(gl.TEXTURE_2D);
			gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR_MIPMAP_LINEAR);
		} else {
			gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
		}
		return texture;
	}

	/** A grid whose vertices carry the depth (and slope) from the data map's red channel, baked once. */
	private geometry(gl: WebGLRenderingContext, data: HTMLImageElement): void {
		const n = SEGMENTS + 1;
		const sample = document.createElement("canvas");
		sample.width = n;
		sample.height = n;
		const context = sample.getContext("2d", { willReadFrequently: true });
		if (!context) throw new Error("2d");
		context.imageSmoothingQuality = "high";
		context.drawImage(data, 0, 0, n, n);
		const pixels = context.getImageData(0, 0, n, n).data;
		const depth = new Float32Array(n * n);
		for (let i = 0; i < n * n; i += 1) depth[i] = pixels[i * 4] / 255;
		const at = (ix: number, iy: number) => depth[Math.min(n - 1, Math.max(0, iy)) * n + Math.min(n - 1, Math.max(0, ix))];
		const position = new Float32Array(n * n * 2);
		const uv = new Float32Array(n * n * 2);
		const gradient = new Float32Array(n * n * 2);
		for (let iy = 0; iy < n; iy += 1) {
			for (let ix = 0; ix < n; ix += 1) {
				const k = iy * n + ix;
				position[k * 2] = ix / SEGMENTS - 0.5;
				position[k * 2 + 1] = 0.5 - iy / SEGMENTS;
				uv[k * 2] = ix / SEGMENTS;
				uv[k * 2 + 1] = 1 - iy / SEGMENTS;
				gradient[k * 2] = ((at(ix + 1, iy) - at(ix - 1, iy)) * SEGMENTS) / 2;
				gradient[k * 2 + 1] = ((at(ix, iy - 1) - at(ix, iy + 1)) * SEGMENTS) / 2;
			}
		}
		const index = new Uint16Array(SEGMENTS * SEGMENTS * 6);
		let o = 0;
		for (let iy = 0; iy < SEGMENTS; iy += 1) {
			for (let ix = 0; ix < SEGMENTS; ix += 1) {
				const a = iy * n + ix;
				const b = a + n;
				index.set([a, b, a + 1, b, b + 1, a + 1], o);
				o += 6;
			}
		}
		this.indexCount = index.length;
		const program = this.program;
		if (!program) throw new Error("program");
		const attribute = (name: string, values: Float32Array, size: number) => {
			const buffer = gl.createBuffer();
			if (!buffer) throw new Error("buffer");
			this.buffers.push(buffer);
			gl.bindBuffer(gl.ARRAY_BUFFER, buffer);
			gl.bufferData(gl.ARRAY_BUFFER, values, gl.STATIC_DRAW);
			const location = gl.getAttribLocation(program, name);
			if (location < 0) return;
			gl.enableVertexAttribArray(location);
			gl.vertexAttribPointer(location, size, gl.FLOAT, false, 0, 0);
		};
		attribute("aPos", position, 2);
		attribute("aUv", uv, 2);
		attribute("aDepth", depth, 1);
		attribute("aGrad", gradient, 2);
		const indices = gl.createBuffer();
		if (!indices) throw new Error("buffer");
		this.buffers.push(indices);
		gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, indices);
		gl.bufferData(gl.ELEMENT_ARRAY_BUFFER, index, gl.STATIC_DRAW);
	}

	private layout(): void {
		const gl = this.gl;
		if (!gl) return;
		const rect = this.host.getBoundingClientRect();
		const ratio = Math.min(window.devicePixelRatio || 1, 1.5);
		const width = Math.max(1, Math.round(rect.width * ratio));
		const height = Math.max(1, Math.round(rect.height * ratio));
		if (this.canvas.width !== width || this.canvas.height !== height) {
			this.canvas.width = width;
			this.canvas.height = height;
		}
		gl.viewport(0, 0, width, height);
	}

	private sync(): void {
		const on = this.loaded && !this.failed && !this.disposed && !this.reduced && this.visible && !document.hidden;
		if (on && !this.running) {
			this.running = true;
			this.last = performance.now();
			const tick = (now: number) => {
				if (!this.running) return;
				this.frame(now);
				if (this.running) this.raf = window.requestAnimationFrame(tick);
			};
			this.raf = window.requestAnimationFrame(tick);
		} else if (!on) {
			this.stop();
		}
	}

	private stop(): void {
		this.running = false;
		if (this.raf) window.cancelAnimationFrame(this.raf);
		this.raf = 0;
	}

	private frame(now: number): void {
		const dt = Math.min(0.05, (now - this.last) / 1000);
		this.last = now;
		// Measured on real frames after the reveal; gaps over 100 ms are a throttled tab, not a slow GPU.
		this.frames += 1;
		if (!this.software && this.frames > 90 && now - this.revealStart > 2500 && document.visibilityState === "visible") {
			if (dt > SLOW_FRAME && dt < 0.1) {
				this.slow += 1;
				if (this.slow > SLOW_LIMIT) {
					this.fail(`slow (${Math.round(1000 / Math.max(dt * 1000, 1))} fps)`);
					return;
				}
			} else if (dt <= SLOW_FRAME) {
				this.slow = Math.max(0, this.slow - 1);
			}
		}
		this.t += dt;
		const k = Math.min(1, dt * 1.6);
		for (let i = 0; i < 3; i += 1) this.lightCol[i] += (this.targetCol[i] - this.lightCol[i]) * k;
		const breath = this.state === "working" ? 0.72 + 0.28 * Math.sin((this.t * Math.PI) / 2) : 1;
		this.light += (this.targetLight * breath - this.light) * Math.min(1, dt * 2.5);
		const tx = this.mx * 0.16 + Math.sin(this.t * 0.25) * 0.05;
		const ty = this.my * 0.08 + Math.sin(this.t * 0.19 + 1) * 0.025;
		this.ry += (tx - this.ry) * Math.min(1, dt * 2);
		this.rx += (ty - this.rx) * Math.min(1, dt * 2);
		this.draw(now);
	}

	private draw(now: number): void {
		const gl = this.gl;
		if (!gl || !this.loaded || this.failed || this.disposed) return;
		if (this.reduced) {
			this.lightCol = [...this.targetCol];
			this.light = this.targetLight;
		}
		const u = this.uniforms;
		const reveal = this.reduced ? 1 : Math.min(1, (now - this.revealStart) / 1400) ** 0.8;
		const glow = this.state === "failed" ? 0.12 : 0.68 + 0.12 * Math.sin(this.t * 0.9) * (this.state === "working" ? 1 : 0.4);
		const aspect = this.canvas.width / Math.max(1, this.canvas.height);
		gl.uniformMatrix4fv(u.uProj, false, perspective((26 * Math.PI) / 180, aspect, 0.1, 20));
		const rotation = rotationXY(this.rx, this.ry);
		gl.uniformMatrix4fv(u.uModelView, false, modelView(rotation, 2.38, -0.02, -5.2));
		gl.uniformMatrix3fv(u.uNormal, false, rotation);
		gl.uniform1f(u.uReveal, reveal);
		gl.uniform3f(u.uLightCol, this.lightCol[0], this.lightCol[1], this.lightCol[2]);
		gl.uniform1f(u.uLight, this.light);
		gl.uniform1f(u.uGlow, glow);
		gl.uniform3f(u.uLightDir, -0.5 + this.ry * 0.6, 0.75, 0.6);
		gl.uniform1f(u.uBreath, Math.sin((this.t * Math.PI) / 2.2));
		const g1 = Math.sin(this.t * 0.9) * 0.5 + 0.5;
		const g2 = Math.sin(this.t * 0.7 + 2.1) * 0.5 + 0.5;
		gl.uniform2f(u.uHand, g1 * 0.006, g2 * 0.006);
		gl.clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT);
		gl.drawElements(gl.TRIANGLES, this.indexCount, gl.UNSIGNED_SHORT, 0);
	}
}

function loadImage(src: string): Promise<HTMLImageElement> {
	return new Promise((resolve, reject) => {
		const image = new Image();
		image.decoding = "async";
		image.onload = () => resolve(image);
		image.onerror = () => reject(new Error(`image: ${src}`));
		image.src = src;
	});
}

/** Column-major 3x3 for rotation X then Y (three.js Euler "XYZ" with z = 0): R = Rx · Ry. */
export function rotationXY(rx: number, ry: number): Float32Array {
	const cx = Math.cos(rx);
	const sx = Math.sin(rx);
	const cy = Math.cos(ry);
	const sy = Math.sin(ry);
	// Rows of Rx·Ry: [cy, 0, sy], [sx·sy, cx, -sx·cy], [-cx·sy, sx, cx·cy]
	return new Float32Array([cy, sx * sy, -cx * sy, 0, cx, sx, sy, -sx * cy, cx * cy]);
}

/** View · Model for a uniformly scaled, rotated mesh at (0, y) seen from a camera at z = -tz. */
function modelView(rotation: Float32Array, scale: number, y: number, tz: number): Float32Array {
	const m = new Float32Array(16);
	for (let column = 0; column < 3; column += 1) {
		for (let row = 0; row < 3; row += 1) m[column * 4 + row] = rotation[column * 3 + row] * scale;
	}
	m[12] = 0;
	m[13] = y;
	m[14] = tz;
	m[15] = 1;
	return m;
}

export function perspective(fovY: number, aspect: number, near: number, far: number): Float32Array {
	const f = 1 / Math.tan(fovY / 2);
	const nf = 1 / (near - far);
	return new Float32Array([f / aspect, 0, 0, 0, 0, f, 0, 0, 0, 0, (far + near) * nf, -1, 0, 0, 2 * far * near * nf, 0]);
}
