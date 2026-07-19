# Deferred production duplicate acceptance

Status: **DEFERRED — requires the actual Linux Trunk Recorder host.**

On that host, choose one completed source audio/metadata pair without changing or deleting it. Record the sender spool count and the destination’s sender-aware matching-call count. Queue the same pair through the production uploader, then confirm it reports success, removes its spool job, leaves one matching destination call and one permanent audio file, and that the original call still returns a `206` range response. Do not place credentials, source paths, or call metadata in tickets or Git.
