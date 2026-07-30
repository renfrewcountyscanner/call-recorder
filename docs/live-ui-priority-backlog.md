# Live UI priority backlog

This backlog is the sanitized v0.4.1 subset of the legacy parity audit. It
contains only safe, Linux-native call-log improvements; later parity work is
not part of this release.

| Item | Release | Priority | Status |
|---|---|---|---|
| Explicit live-update pause/resume with queued-call indication | v0.4.1 | Critical | Implemented |
| Safe bounded page-size selector (25/50/100/250) | v0.4.1 | High | Implemented |
| Print-friendly call list and call-detail output | v0.4.1 | High | Implemented |
| SmartSort-style ordering | v0.4.2 | Medium | Deferred |
| User-configurable grid columns | v0.5.0 | Low | Deferred |
| Authenticated legacy settings parity | Later | Low | Deferred |
| Streaming mounts and queue controls | Later | Low | Deferred |

The v0.4.1 items preserve query-string filters, playback state, bounded
pagination, and the existing Linux security model. No proprietary interface
or implementation material is used.
