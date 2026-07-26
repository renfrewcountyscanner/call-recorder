# Accessibility

The web interface is built for practical accessibility:

- Semantic HTML: header/nav/main landmarks, tables with `th scope="col"`, labelled sections, skip-to-content link.
- Every form control has an associated label; icon buttons carry `aria-label`s.
- Keyboard operation throughout: visible `:focus-visible` rings, playback shortcuts (Space/N/P/S/M) that suspend while typing, native `<dialog>` for destructive confirmations.
- Status is never colour-only: badges and the health dot pair colour with text or labels; playback changes are announced through an `aria-live` region.
- Contrast meets or exceeds 4.5:1 for text in both themes; `prefers-reduced-motion` disables animation; touch targets are at least 44 px.
- ARIA is used only where native semantics are insufficient (current-page indication, live regions, icon-only controls).

## Automated checks

`tests/accessibility.sh` runs vendored axe-core against the isolated environment on port 18080: call list, call detail, admin login, talkgroup/radio/retention administration, retention history, and the unauthorized-administration state. The suite fails on any critical violation. Run it after interface changes.
