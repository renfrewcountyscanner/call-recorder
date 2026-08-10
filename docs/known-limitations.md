# Known limitations and follow-up work

Call Logger v1.0.0 includes Linux ingestion and a supported Windows ProScan directory uploader. The remaining limitations are:

- Local sign-in throttling is per backend process. Keep internet-facing deployments behind a trusted access proxy as well as application authentication.
- Cloudflare Access identity headers are accepted only from configured trusted proxy addresses; deployments must prevent clients from reaching the origin around that proxy.
- Call-list CSV and audio exports run in the requesting connection. Use the background dataset exporter for large model-training exports.
- Alias CSV import is not transactional; a mid-file failure can leave a partial import that is safe to re-run.
- A retention worker killed after staging audio can leave recoverable files under `.retention-trash`; database call rows are preserved in that failure mode.
- The manual legacy-directory importer reads each bounded MP3/WAV file into memory before upload. Its configured maximum file size should remain conservative.
- Notification delivery supports one recipient per SMTP destination. Create multiple destinations when several recipients are required.
- Operational credential rotation and recovery drills still require an administrator-run procedure.
