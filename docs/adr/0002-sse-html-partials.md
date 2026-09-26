# Server push HTML partials via SSE

Pipeline events are pushed as HTML fragments through SSE, rendered by the server. A minimal client-side handler swaps them into the DOM by event type: `step_log` appends to timeline, `progress` updates progress bar, `job_complete` shows downloads.

Other options considered:
- JSON over SSE + client-side templating — duplicates rendering logic, creates two sources of truth for HTML structure
- Polling — wasteful for a pipeline that may take minutes
