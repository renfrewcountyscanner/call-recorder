# Web interface

The v0.3.0 web interface is a server-rendered Go-template application with vendored HTMX for partial refresh and vanilla JavaScript for playback. All assets are self-contained in the backend binary (`/static/…`); there are no CDN dependencies. Dark theme is the default; a light theme and a system-following option are available from the header toggle (persisted in the browser).

## Navigation

The header shows the application name and version, a backend health indicator (green when `/healthz` answers), the theme toggle, and navigation. **Calls** is always visible. **Talkgroups**, **Radios**, **Retention**, and **History** appear only when administration is enabled *and* the current session is authorized; otherwise an **Admin sign-in** link appears. On narrow screens the navigation collapses behind a menu button.

## Call log

The call log is the primary page. Talkgroup and radio aliases are the primary labels, with numeric IDs shown as secondary chips. Each row shows time, system/site, talkgroup, radio, frequency, duration, and call-type/patch badges. On screens below 768 px the table becomes stacked call cards with labeled fields. The currently playing row is highlighted and never changes size.

## Filtering

Filters include free-text search (alias, ID, or transcript), sender, system, talkgroup, radio ID, a from/to date range, and page size. Typing updates the list after a short delay; the Apply button submits immediately. Active filters appear as removable chips, values are preserved after submission, and the address bar always mirrors the current filter state, so any view can be shared as a `/ ?key=value` URL. The legacy exact-day `date` parameter is still accepted. Results show a total count and paginate with Newer/Older controls.

## Playback

A single shared player bar appears at the bottom when a call is started from a row play button or the call-detail page. It provides play/pause, previous, next, stop, a seek bar with current/total time, an auto-advance toggle (sequential playback, on by default), playback speed (0.75×–2×, remembered for the browser session), and volume. Only one call can play at a time, and audio never starts without a user action. Sequential playback follows the currently filtered list. Keyboard controls (when not typing in a field): Space = play/pause, N = next, P = previous, S = stop, M = mute. Byte-range streaming is unchanged.

## Call detail

The detail page groups call information into talkgroup, radio, system/site, and timing/signal sections with links to filtered call lists and (when authorized) alias administration. Raw preserved metadata is shown in a collapsible, pretty-printed section. Absolute filesystem paths and credentials are never displayed.

## Browser support

Current Chromium, Firefox, and Safari on desktop and mobile. JavaScript is required for filtering and playback; the call list degrades to the paginated server-rendered fragment.
