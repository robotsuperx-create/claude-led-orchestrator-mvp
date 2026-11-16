---
name: web-essentials
description: Public websites. Search, sharing and loading speed handled from the start.
---
- Every page has a unique <title> under 60 characters and a meta description under 160, in the page's language.
- Open Graph and Twitter card tags (title, description, image path) so shared links look right.
- One h1, then headings in order; meaningful link text, never "click here".
- A favicon (an inline SVG is fine) and a theme-color meta tag.
- Structured data (schema.org JSON-LD) when it fits: Organization, Product, LocalBusiness, FAQPage.
- Speed: defer scripts, preload the main font, lazy-load below-the-fold images with width and height set to avoid layout shift, keep the total page light.
- Use relative links for local assets so the site works when opened from disk and when hosted.
