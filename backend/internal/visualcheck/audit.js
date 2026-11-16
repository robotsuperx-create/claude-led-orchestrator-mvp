// Runs inside the rendered page. It scrolls once through the page so content
// that appears on scroll is revealed for the screenshot, waits for fonts, and
// reports what a visitor would notice. Findings are short English sentences
// for the critic; at most a few of each kind.
(async (mobile, screenWidth) => {
	// A phone browser zooms out to fit content wider than the screen, so the
	// window can report the content's width; the screen's is what counts.
	const innerWidth = screenWidth || window.innerWidth;
	const sleep = (ms) => new Promise((done) => setTimeout(done, ms));
	const findings = [];
	const limit = (list, n) => list.slice(0, n);
	const describe = (el) => {
		let label = el.tagName.toLowerCase();
		if (el.id) label += "#" + el.id;
		else if (typeof el.className === "string" && el.className.trim()) label += "." + el.className.trim().split(/\s+/)[0];
		const text = (el.innerText || el.getAttribute("aria-label") || el.getAttribute("alt") || "").trim().replace(/\s+/g, " ");
		return text ? `${label} ("${text.slice(0, 40)}")` : label;
	};
	const visible = (el) => {
		const r = el.getBoundingClientRect();
		const s = getComputedStyle(el);
		return r.width > 0 && r.height > 0 && s.visibility !== "hidden" && s.display !== "none" && Number(s.opacity) > 0.05;
	};

	try { await document.fonts.ready; } catch (e) {}
	const step = Math.max(200, Math.floor(innerHeight * 0.8));
	for (let y = 0; y < document.documentElement.scrollHeight; y += step) {
		scrollTo(0, y);
		await sleep(140);
	}
	scrollTo(0, 0);
	await sleep(400);

	const root = document.documentElement;
	if (root.scrollWidth > innerWidth + 1) {
		const wide = [...document.body.querySelectorAll("*")].filter((el) => visible(el) && el.getBoundingClientRect().right > innerWidth + 1);
		findings.push(`The page scrolls sideways: it is ${root.scrollWidth}px wide in a ${innerWidth}px window` + (wide.length ? `, widest: ${limit(wide, 3).map(describe).join(", ")}` : "") + ".");
	}

	const spilled = [...document.body.querySelectorAll("p, h1, h2, h3, h4, h5, h6, a, button, span, li, label, td")]
		.filter((el) => visible(el) && el.children.length === 0 && el.textContent.trim() && el.scrollWidth > el.clientWidth + 2 && getComputedStyle(el).overflowX === "visible");
	for (const el of limit(spilled, 3)) findings.push(`Text spills out of its box: ${describe(el)}.`);

	for (const img of limit([...document.images].filter((img) => img.complete && img.naturalWidth === 0), 3)) {
		findings.push(`An image does not load: ${img.getAttribute("src") || "(no src)"}.`);
	}
	const noAlt = [...document.images].filter((img) => !img.hasAttribute("alt")).length;
	if (noAlt) findings.push(`${noAlt} image(s) have no alt text.`);

	const h1 = document.querySelectorAll("h1").length;
	if (h1 !== 1) findings.push(h1 === 0 ? "The page has no h1 heading." : `The page has ${h1} h1 headings; one is expected.`);

	const parse = (value) => {
		const m = value.match(/rgba?\(([^)]+)\)/);
		if (!m) return null;
		const [r, g, b, a = "1"] = m[1].split(/[ ,/]+/).filter(Boolean);
		return [Number(r), Number(g), Number(b), Number(a)];
	};
	const luminance = ([r, g, b]) => {
		const c = [r, g, b].map((v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; });
		return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
	};
	const background = (el) => {
		for (let node = el; node && node.nodeType === 1; node = node.parentElement) {
			const s = getComputedStyle(node);
			if (s.backgroundImage && s.backgroundImage !== "none") return null; // over an image or gradient: not measurable here
			const c = parse(s.backgroundColor);
			if (c && c[3] > 0.9) return c;
		}
		return [255, 255, 255, 1];
	};
	const lowContrast = [];
	const texts = [...document.body.querySelectorAll("p, h1, h2, h3, h4, h5, h6, a, button, span, li, label, small, td")].slice(0, 600);
	for (const el of texts) {
		if (lowContrast.length >= 5) break;
		if (!visible(el) || ![...el.childNodes].some((n) => n.nodeType === 3 && n.textContent.trim())) continue;
		const s = getComputedStyle(el);
		const fg = parse(s.color), bg = background(el);
		if (!fg || !bg || fg[3] < 0.5) continue;
		const [hi, lo] = [luminance(fg), luminance(bg)].sort((a, b) => b - a);
		const ratio = (hi + 0.05) / (lo + 0.05);
		const size = parseFloat(s.fontSize), bold = Number(s.fontWeight) >= 700;
		const needed = size >= 24 || (size >= 18.66 && bold) ? 3 : 4.5;
		if (ratio < needed) lowContrast.push(`Low contrast ${ratio.toFixed(1)}:1 (needs ${needed}:1): ${describe(el)}.`);
	}
	findings.push(...lowContrast);

	if (mobile) {
		const small = [...document.querySelectorAll("a, button, input, select, textarea, [role=button]")]
			.filter((el) => { if (!visible(el)) return false; const r = el.getBoundingClientRect(); return r.width < 40 || r.height < 40; });
		for (const el of limit(small, 4)) {
			const r = el.getBoundingClientRect();
			findings.push(`Tap target too small (${Math.round(r.width)}x${Math.round(r.height)}px, 44px wanted): ${describe(el)}.`);
		}
	}

	return { height: Math.max(root.scrollHeight, document.body ? document.body.scrollHeight : 0), findings };
})
