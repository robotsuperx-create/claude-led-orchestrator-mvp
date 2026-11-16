---
name: code-quality
description: Any code change. Fit the codebase, keep changes small and correct, and leave tests that can fail.
---
- Read the surrounding code first and follow its conventions: naming, structure, error handling, formatting, file layout. Extend what exists before adding something new.
- Make the smallest change that fully solves the task. Do not reformat or rename unrelated code.
- Handle errors explicitly; never swallow them silently. Validate input at boundaries.
- Keep functions short and named for what they do. Comments explain why, not what.
- When behaviour changes, add or update a test that would fail without the change. Do not weaken or delete tests to make them pass.
- Never commit secrets, keys or personal data. Never disable security checks.
- In web pages, keep HTML semantic, CSS in stylesheets, scripts deferred, and no console errors.
