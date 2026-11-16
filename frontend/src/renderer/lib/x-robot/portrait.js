// Vendored from the X-ROBOT trading dashboard (dashboard/static/ui/portrait.js) so X-00 on Home is the
// same commander, drawn by the same code. Kept as written; the only changes are marked "X-ROBOT-AI:" —
// the art comes from the app's bundled images, and dispose() lets a React screen unmount him cleanly.
// portrait.js — X-00, the one living thing on the deck. A relief portrait (depth baked into the mesh) drawn in a
// single pass with no post-processing: desaturated chrome, lit only by the light in the room — ion while he watches,
// breathing while he decides, the decision colour when he has decided, dark when a veto stands.
// Target: 60 fps on an integrated laptop GPU; if it cannot hold 55, the still image takes over.
import * as THREE from "three";

export const REDUCED = matchMedia("(prefers-reduced-motion: reduce)").matches;
// X-ROBOT-AI: the art is bundled with the app (the same files: x00h.webp and x00h_d.webp).
import x00Art from "../../assets/x00/x00.webp";
import x00Data from "../../assets/x00/x00-data.webp";
const ARTS = { x00h: [x00Art, x00Data] };
const loader = new THREE.TextureLoader(), texCache = new Map(), geoCache = new Map();
function loadTex(url, color) {
  if (!texCache.has(url)) texCache.set(url, new Promise((res, rej) => loader.load(url, (t) => { t.colorSpace = color ? THREE.SRGBColorSpace : THREE.NoColorSpace; t.anisotropy = 4; t.minFilter = THREE.LinearMipmapLinearFilter; t.magFilter = THREE.LinearFilter; res(t); }, undefined, rej)));
  return texCache.get(url);
}
const loadArt = (name) => Promise.all([loadTex(ARTS[name][0], true), loadTex(ARTS[name][1], false)]);   // X-ROBOT-AI: bundled art
// a plane of seg×seg cells whose vertices carry the depth (and slope) sampled from the data map's red channel —
// baked once: vertex texture fetches are slow on laptop GPUs, and the relief never changes shape after load.
function reliefGeometry(name, dataTex, seg) {
  const key = `${name}@${seg}`; if (geoCache.has(key)) return geoCache.get(key);
  const n = seg + 1, cv = document.createElement("canvas"); cv.width = cv.height = n;
  const x = cv.getContext("2d", { willReadFrequently: true }); x.imageSmoothingQuality = "high"; x.drawImage(dataTex.image, 0, 0, n, n);
  const px = x.getImageData(0, 0, n, n).data, d = new Float32Array(n * n);
  for (let i = 0; i < n * n; i++) d[i] = px[i * 4] / 255;
  const g = new THREE.PlaneGeometry(1, 1, seg, seg), grad = new Float32Array(n * n * 2), at = (ix, iy) => d[Math.min(n - 1, Math.max(0, iy)) * n + Math.min(n - 1, Math.max(0, ix))];
  for (let iy = 0; iy < n; iy++) for (let ix = 0; ix < n; ix++) { const k = iy * n + ix; grad[k * 2] = (at(ix + 1, iy) - at(ix - 1, iy)) * seg / 2; grad[k * 2 + 1] = (at(ix, iy - 1) - at(ix, iy + 1)) * seg / 2; }
  g.setAttribute("aDepth", new THREE.BufferAttribute(d, 1)); g.setAttribute("aGrad", new THREE.BufferAttribute(grad, 2)); g.deleteAttribute("normal");
  geoCache.set(key, g); return g;
}

const COLORS = { watching: "#C6E3F0", deciding: "#C6E3F0", long: "#9FE3BD", short: "#F0A58F", veto: "#ECE9E2" };
const LIGHT = { watching: .55, deciding: .55, long: 1, short: 1, veto: .12 };       // how much light there is
const VERT = /* glsl */ `
attribute float aDepth; attribute vec2 aGrad;
uniform float uRelief; uniform float uReveal; uniform vec2 uHand; uniform float uBreath; uniform vec4 uHandBox;
varying vec2 vUv; varying vec3 vN; varying vec3 vView;
float box(vec2 uv, vec2 lo, vec2 hi, float soft){ vec2 a = smoothstep(lo - soft, lo + soft, uv), b = smoothstep(hi + soft, hi - soft, uv); return a.x * a.y * b.x * b.y; }
void main(){
  vUv = uv; float k = uRelief * uReveal;
  vec3 p = position; p.z += aDepth * k;
  // alive: the chest breathes, the hands lift and settle (more when he talks)
  float chest = smoothstep(.2, .62, uv.y) * smoothstep(.95, .7, uv.y);
  p.y += chest * uBreath * .006;
  float hl = box(uv, vec2(0., uHandBox.x), vec2(uHandBox.y, uHandBox.z), .06), hr = box(uv, vec2(1. - uHandBox.y, uHandBox.x), vec2(1., uHandBox.z), .06);
  p.y += hl * uHand.x + hr * uHand.y;
  p.x += (hl * uHand.x - hr * uHand.y) * .25;
  vN = normalize(normalMatrix * normalize(vec3(-aGrad.x * k, -aGrad.y * k, 1.)));
  vec4 mv = modelViewMatrix * vec4(p, 1.); vView = normalize(-mv.xyz);
  gl_Position = projectionMatrix * mv;
}`;
const FRAG = /* glsl */ `
uniform sampler2D uMap; uniform sampler2D uData;
uniform vec3 uLightCol; uniform vec3 uLightDir; uniform float uLight; uniform float uGlow; uniform float uReveal; uniform float uSat;
uniform float uMouth; uniform vec2 uMouthAt; uniform float uMouthW;
varying vec2 vUv; varying vec3 vN; varying vec3 vView;
void main(){
  vec2 uv = vUv;
  // he speaks: the jaw drops with the voice — the lower face slides down and a dark mouth opens on the lip line
  float jawW = smoothstep(uMouthW * 2.2, uMouthW * 1.1, abs(uv.x - uMouthAt.x));
  float jawY = smoothstep(uMouthAt.y - .09, uMouthAt.y - .03, uv.y) * step(uv.y, uMouthAt.y);
  float drop = uMouth * .027 * jawW * jawY;
  uv.y += drop;
  float cav = smoothstep(uMouthAt.y - drop - .002, uMouthAt.y - drop + .002, vUv.y) * smoothstep(uMouthAt.y + .002, uMouthAt.y - .002, vUv.y) * jawW * step(.001, drop);
  vec4 c = texture2D(uMap, uv); vec4 D = texture2D(uData, uv);
  float a = c.a * smoothstep(0., .03, vUv.y) * smoothstep(0., .08, uReveal * 1.25 - (1. - vUv.y));
  if (a < .004) discard;
  vec3 col = c.rgb; float lum = dot(col, vec3(.2126, .7152, .0722));
  col = mix(col, vec3(lum), uSat);                                   // chrome, not colour
  vec3 N = normalize(vN);
  float lam = clamp(dot(N, normalize(uLightDir)), 0., 1.);
  col *= (.22 + .78 * lam) * (.35 + .9 * uLight);                     // the room is dark; the light is the data
  col *= mix(vec3(1.), uLightCol, .3 * uLight);
  float ndv = clamp(abs(dot(N, normalize(vView))), 0., 1.);
  col += vec3(.93, .91, .89) * pow(max(1. - ndv, 0.), 3.) * .08;     // a faint ivory rim keeps him in the room when the light goes
  float g = D.g;                                                      // the eyes, the crest and the body lights carry the light itself
  col = mix(col, uLightCol * (.18 + .85 * lum) * uGlow, g);
  // the open mouth: dark inside, a thread of the room's light where the voice is
  float mid = 1. - abs((vUv.y - (uMouthAt.y - drop * .5)) / max(drop * .5, .001));
  col = mix(col, vec3(.015, .016, .018) + uLightCol * .22 * max(mid, 0.) * uGlow, cav);
  gl_FragColor = vec4(col, a);
}`;

export class Portrait {
  constructor(host, { art = "x00h", state = "watching", still = null } = {}) {
    this.host = host; this.state = state; this.ok = false;
    const canvas = document.createElement("canvas"); this.canvas = canvas;
    const halo = host.querySelector(".halo"); if (halo) host.insertBefore(canvas, halo.nextSibling); else host.appendChild(canvas);
    if (still) { const img = document.createElement("img"); img.src = still; img.alt = ""; img.hidden = true; host.appendChild(img); this.img = img; }
    let R;
    try { R = new THREE.WebGLRenderer({ canvas, antialias: false, alpha: true, premultipliedAlpha: false, powerPreference: "high-performance" }); }
    catch (e) { this.fallback("no webgl"); return; }
    this.R = R; R.setPixelRatio(Math.min(devicePixelRatio || 1, 1.5));
    const dbg = R.getContext().getExtension("WEBGL_debug_renderer_info");
    this.soft = !!dbg && /swiftshader|llvmpipe|software/i.test(R.getContext().getParameter(dbg.UNMASKED_RENDERER_WEBGL) || "");   // no fps verdicts on software GL R.outputColorSpace = THREE.SRGBColorSpace; R.toneMapping = THREE.NoToneMapping; R.setClearColor(0x000000, 0);
    this.scene = new THREE.Scene(); this.camera = new THREE.PerspectiveCamera(26, 1, .1, 20); this.camera.position.z = 5.2;
    this.mat = new THREE.ShaderMaterial({ vertexShader: VERT, fragmentShader: FRAG, transparent: true, depthWrite: true,
      uniforms: { uMap: { value: null }, uData: { value: null }, uRelief: { value: .3 }, uReveal: { value: REDUCED ? 1 : 0 },
        uLightCol: { value: new THREE.Color(COLORS[state]) }, uLightDir: { value: new THREE.Vector3(-.5, .75, .6) }, uLight: { value: LIGHT[state] }, uGlow: { value: 1 }, uSat: { value: .9 },
        uMouth: { value: 0 }, uMouthAt: { value: new THREE.Vector2(.5, .687) }, uMouthW: { value: .036 }, uHand: { value: new THREE.Vector2(0, 0) }, uBreath: { value: 0 }, uHandBox: { value: new THREE.Vector4(.03, .33, .3, 0) } } });
    this.voiceSrc = null; this.mouth = 0; this.speaking = false;
    this.bust = new THREE.Mesh(new THREE.PlaneGeometry(1, 1), this.mat); this.bust.visible = false; this.bust.scale.setScalar(2.38); this.bust.position.y = -.02; this.scene.add(this.bust);
    loadArt(art).then(([map, data]) => {
      if (this.disposed) return;   // X-ROBOT-AI: he left before his art arrived
      this.bust.geometry = reliefGeometry(art, data, 200);
      this.mat.uniforms.uMap.value = map; this.mat.uniforms.uData.value = data; this.bust.visible = true; this.t0 = performance.now(); host.classList.add("ready");
    }).catch(() => this.fallback("art"));
    this.targetCol = new THREE.Color(COLORS[state]); this.targetLight = LIGHT[state];
    this.visible = true; this.frames = 0; this.slow = 0; this.last = performance.now(); this.lastDraw = 0; this.t = 0; this.rx = 0; this.ry = 0;
    this.mx = 0; this.my = 0;
    // X-ROBOT-AI: the same listeners, kept so dispose() can remove them
    this.onMove = (e) => { this.mx = e.clientX / innerWidth - .5; this.my = e.clientY / innerHeight - .5; };
    this.onVis = () => { this.slow = 0; this.loop(this.visible && !document.hidden); };
    addEventListener("pointermove", this.onMove, { passive: true });
    this.ro = new ResizeObserver(() => this.layout()); this.ro.observe(host);
    this.io = new IntersectionObserver(([e]) => { this.visible = e.isIntersecting; this.loop(this.visible && !document.hidden); }); this.io.observe(host);
    document.addEventListener("visibilitychange", this.onVis);
    this.layout(); this.ok = true; this.loop(true);
  }
  // X-ROBOT-AI: let the screen that showed him take him down (listeners, observers, the GL context)
  dispose() {
    this.disposed = true; this.loop(false);
    if (this.onMove) removeEventListener("pointermove", this.onMove);
    if (this.onVis) document.removeEventListener("visibilitychange", this.onVis);
    this.ro?.disconnect(); this.io?.disconnect();
    if (this.R) { this.mat?.dispose(); this.R.dispose(); this.R.forceContextLoss?.(); }
    this.canvas?.remove(); this.img?.remove();
  }
  fallback(why) { this.host.classList.add("no-gl"); if (this.img) this.img.hidden = false; if (this.R) { this.loop(false); this.canvas.remove(); } console.info("[x-robot] X-00 portrait: still image (" + why + ")"); }
  layout() {
    if (!this.R) return;
    const r = this.host.getBoundingClientRect(), w = Math.round(r.width), h = Math.round(r.height); if (!w || !h) return;
    this.R.setSize(w, h, false); this.camera.aspect = w / h; this.camera.updateProjectionMatrix();
  }
  setVoice(src) { this.voiceSrc = src; }                 // () => 0..1, the envelope of what he is saying
  setSpeaking(on) { this.speaking = !!on; }
  setState(s) {
    if (!COLORS[s]) return;
    this.state = s; this.targetCol.set(COLORS[s]); this.targetLight = LIGHT[s];
    ["watching", "deciding", "long", "short", "veto"].forEach((k) => this.host.classList.toggle(k, k === s));
    this.host.style.setProperty("--light", COLORS[s]);
  }
  loop(on) {
    if (!this.R) return;
    if (on && !this.running) { this.running = true; this.last = performance.now(); this.R.setAnimationLoop((n) => this.frame(n)); }
    else if (!on && this.running) { this.running = false; this.R.setAnimationLoop(null); }
  }
  frame(now) {
    const dt = Math.min(.05, (now - this.last) / 1000); this.last = now;
    // the budget: 55 fps or the still image takes over (measured on real frames, after the reveal)
    // (gaps over 100 ms are the browser throttling a hidden tab, not a slow GPU — they never count)
    if (!this.soft && this.frames++ > 90 && this.t0 && now - this.t0 > 2500 && document.visibilityState === "visible") {
      if (dt > 1 / 52 && dt < .1) { if (++this.slow > 40) return this.fallback(`slow (${Math.round(1000 / Math.max(dt, .001))} fps)`); } else if (dt <= 1 / 52) this.slow = Math.max(0, this.slow - 1);
    }
    if (!REDUCED) this.t += dt;
    const U = this.mat.uniforms, k = Math.min(1, dt * 1.6);          // the light changes at breathing pace (~1.2 s)
    U.uLightCol.value.lerp(this.targetCol, k);
    const breath = this.state === "deciding" ? .72 + .28 * Math.sin(this.t * Math.PI / 2) : 1;
    U.uLight.value += (this.targetLight * breath - U.uLight.value) * Math.min(1, dt * 2.5);
    U.uGlow.value = this.state === "veto" ? .12 : .68 + .12 * Math.sin(this.t * .9) * (this.state === "deciding" ? 1 : .4);
    if (this.t0) U.uReveal.value = REDUCED ? 1 : Math.min(1, (now - this.t0) / 1400) ** .8;
    // he is alive: a slow sway, he turns a little toward you, breathes, and his hands never quite rest
    const lv = this.voiceSrc ? Math.max(0, Math.min(1, this.voiceSrc(now))) : 0;
    this.mouth += ((this.speaking ? lv : 0) - this.mouth) * Math.min(1, dt * 22);
    U.uMouth.value = this.mouth;
    const talkK = this.speaking ? 1 : 0;
    const tx = this.mx * .16 + Math.sin(this.t * .25) * .05, ty = this.my * .08 + Math.sin(this.t * .19 + 1) * .025 + talkK * Math.sin(this.t * 2.3) * .012 * lv;
    this.ry += (tx - this.ry) * Math.min(1, dt * 2); this.rx += (ty - this.rx) * Math.min(1, dt * 2);
    this.bust.rotation.set(this.rx, this.ry, 0);
    U.uBreath.value = Math.sin(this.t * Math.PI / 2.2);
    const g1 = Math.sin(this.t * .9) * .5 + .5, g2 = Math.sin(this.t * .7 + 2.1) * .5 + .5;
    U.uHand.value.set(g1 * .006 + talkK * lv * .018 * (.5 + .5 * Math.sin(this.t * 3.1)), g2 * .006 + talkK * lv * .018 * (.5 + .5 * Math.sin(this.t * 2.6 + 1.3)));
    U.uLightDir.value.set(-.5 + this.ry * .6, .75, .6);
    this.R.render(this.scene, this.camera);
  }
}
