---
name: responsive-accessible
description: Every page. Works on a 390 px phone, a tablet and a wide desktop, and for keyboard and screen-reader users.
---
Responsive
- Mobile first: base styles for about 390 px, then min-width media queries at about 768 and 1100 px.
- Fluid type and space with clamp(), for example font-size: clamp(2rem, 5vw + 1rem, 4.5rem).
- No horizontal scrolling at any width. Images and SVG get max-width: 100% and height: auto.
- Tap targets at least 44 by 44 px with space between them. Navigation collapses into a clear menu button on phones.
- Test mentally at 390, 768 and 1440 px: nothing overlaps, nothing is cut off, line lengths stay readable.

Accessible
- Semantic structure: header, nav, main, section with headings in order, footer. One h1 per page.
- Text contrast at least 4.5:1 (3:1 for large text and for icons that carry meaning).
- Every interactive element is reachable and usable by keyboard, with a visible focus style.
- Images have meaningful alt text, or empty alt when decorative. Form fields have labels; errors are described in text.
- Do not convey meaning by colour alone.
