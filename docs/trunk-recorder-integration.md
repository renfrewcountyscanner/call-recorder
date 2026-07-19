# Linux Trunk Recorder integration

Call Recorder accepts completed calls only; Trunk Recorder remains responsible for decoding and recording. Install the sender package on each recorder, copy `uploader/trunk-recorder/example.env` to a mode-600 external configuration file, and invoke `upload_call.py --env /etc/call-recorder-uploader.env --audio "$1" --metadata "$2"` from the completed-call hook.

Set `DESTINATION_URL`, `UPLOAD_ID`, `UPLOAD_KEY`, `SYSTEM_NAME`, `SPOOL_DIR`, `RETRY_COUNT`, and `TIMEOUT_SECONDS`. Each recorder needs a distinct sender credential. The sender writes a pending manifest before its first network request, retries temporary failures with bounded exponential backoff, and moves exhausted items to `failed/`; it never deletes the source audio.

For the currently deployed legacy adapter, use the legacy body-authentication endpoint. Modern API support remains available for Go uploader clients. Do not put secrets in source files, shell history, or Git.
