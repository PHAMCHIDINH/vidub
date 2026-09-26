# vidub

A video dubbing tool that downloads YouTube videos, translates transcripts, generates TTS audio, and produces dubbed output. The frontend is built with HTMX (hypermedia-driven, multi-page).

## Language

**Job**: A video dubbing task, identified by its YouTube VideoID. Contains source URL, dubbing mode, voice selection, API key, and working directory.
_Avoid_: Task, process, dub

**Pipeline**: The processing chain for a Job: extract ∥ download → translate → tts → dub.
_Avoid_: Workflow, flow

**Step**: A single phase within the Pipeline. Each step has a status: started, completed, or failed.
_Avoid_: Stage, phase

**JobStatus**: The overall state of a Job: running, completed, or failed.
_Avoid_: State

**Result**: The output artifacts of a completed Job: voiceover.mp4 (Voiceover mode) or dub.mp4 (Replace mode), its speech track (narrative.mp3 or dub.mp3), and subtitle.srt.
_Avoid_: Output, files

## Rules

- The frontend uses HTMX for all server interactions. Navigation between pages uses `hx-push-url`.
- Job state is stored in an in-memory job store (map + sync.RWMutex). The server is the single source of truth.
- Each Job page (`/jobs/:id`) establishes its own SSE connection for real-time pipeline events via HTML partials.
- Sidebar job history is rendered server-side and polled via `hx-get`.

## ADRs

- [ADR-0001](./docs/adr/0001-multi-page-htmx.md): Multi-page HTMX with hx-push-url
- [ADR-0002](./docs/adr/0002-sse-html-partials.md): Server push HTML partials via SSE
- [ADR-0003](./docs/adr/0003-in-memory-job-store.md): In-memory job store
