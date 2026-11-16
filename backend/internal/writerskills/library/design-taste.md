---
name: design-taste
description: Any user-facing page or screen. Commit to one strong visual direction so the result looks designed, not templated.
---
Decide the direction before writing markup, and write it as a comment at the top of the main stylesheet: mood in three words, palette, type pairing, one signature detail.

Direction
- One idea carried everywhere: a brand's own world (materials, era, place, product), not "modern and clean".
- One dominant colour, one accent used sparingly for the single most important action, neutrals for everything else. Write colours as hex values in CSS variables.
- Two typefaces at most: a characterful display face for headings and a highly readable text face. Load them from Google Fonts with font-display: swap.
- A clear type scale (for example 14, 16, 20, 28, 40, 56, 72 px) and line lengths of 55 to 75 characters.
- Generous, consistent space on an 8 px rhythm. Sections breathe; related items sit close.

What makes a page look like a template, so avoid it
- Purple-to-blue gradients, glassmorphism everywhere, emoji as icons, stock "hero with laptop" compositions.
- Everything centred, every section the same height, three identical cards in a row as the only layout idea.
- Grey text on grey, low-contrast buttons, shadows on everything, borders and shadows and gradients on the same element.
- Lorem ipsum, "Feature 1", placeholder names, fake statistics.

What makes it look crafted
- Asymmetry with intent: an oversized headline, an image or illustration that breaks the grid, a section that changes rhythm.
- Real hierarchy: one thing per section is clearly most important.
- Details: consistent corner radii, aligned baselines, hover and focus states on every interactive element, a footer as considered as the hero.
- Content that sounds like the brand, written for the real audience.

Before finishing, look at the page as a stranger: what is the first thing the eye lands on, and is it the thing that matters most?
