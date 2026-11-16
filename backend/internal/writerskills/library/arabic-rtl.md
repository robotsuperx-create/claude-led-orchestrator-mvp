---
name: arabic-rtl
description: Any interface or content in Arabic, or bilingual Arabic and English. Right-to-left layout and Arabic typography done properly.
---
- Set <html lang="ar" dir="rtl"> for Arabic pages. For bilingual pages switch both lang and dir together.
- Use logical CSS properties everywhere: margin-inline-start, padding-inline-end, inset-inline-start, text-align: start, border-start-start-radius. Never hard-code left and right for layout.
- Mirror direction-dependent icons (arrows, chevrons, progress) in RTL; never mirror logos, media controls or numbers.
- Arabic typefaces with good weights: IBM Plex Sans Arabic, Noto Kufi Arabic or Readex Pro for interfaces; Amiri or Noto Naskh Arabic for long reading; Reem Kufi or Lalezar for display headings. Pair with a Latin face of similar weight.
- Arabic needs more line height (about 1.7 to 1.9 for body text) and slightly larger sizes than Latin at the same visual weight.
- Never apply letter-spacing or text-transform to Arabic; it breaks the joined script.
- Western digits (0-9) are fine and common in modern interfaces; keep numbers, prices and codes in a dir="ltr" span when they mix with Arabic.
- Write natural Modern Standard Arabic, short and warm; do not machine-translate English idioms.
