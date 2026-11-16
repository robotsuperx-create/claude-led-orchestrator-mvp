---
name: motion
description: Pages that should feel alive. Purposeful animation and micro-interactions that stay smooth and respectful.
---
- Animate only transform and opacity, so motion stays at 60 frames per second. Never animate width, height, top or left.
- Durations: 150 to 250 ms for hover and press, 300 to 600 ms for entrances, up to 900 ms for a hero reveal. Ease out for entrances (cubic-bezier(.2,.8,.2,1)), ease in for exits.
- Reveal sections as they scroll in with IntersectionObserver: a short rise (12 to 24 px) and fade, staggered by 60 to 90 ms across children. Reveal once; do not replay on every scroll.
- One signature motion per page (a hero headline that assembles, a product that floats, a counter that rolls) is worth more than motion everywhere.
- Every button and link responds: a hover change and a pressed state. Focus states never animate away.
- Wrap all non-essential motion in @media (prefers-reduced-motion: no-preference), and make the page complete without it.
- No scroll-jacking, no autoplaying sound, no animation that delays reading the content.
