---
name: design-system
description: Pages or apps with several sections or screens. Build from tokens and reusable components so everything stays consistent.
---
- Define tokens once in :root as CSS custom properties: --color-*, --space-1..8 (4 or 8 px steps), --radius-*, --font-*, --text-* sizes, --shadow-*, --ease-*. Use only tokens in component styles; no stray hex values or pixel sizes.
- Provide a dark theme only when asked; if you do, redefine tokens, never component rules.
- Build components with every state: default, hover, focus-visible (a clear 2 px outline in the accent), active, disabled, loading, empty, error.
- Name classes by role (card, card__title, button--primary), not by look (red-text, big-box).
- One button hierarchy: primary (one per view), secondary, quiet. Same height and radius across the page.
- Layout with CSS grid and flex; a max content width (about 1200 px) with side padding that shrinks on small screens.
- Keep CSS in a predictable order: tokens, base, layout, components, utilities, media queries.
- When you edit an existing project, read its tokens and components first and extend them; never introduce a second system beside the first.
