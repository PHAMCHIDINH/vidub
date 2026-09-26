# In-memory job store

Job state is stored in a `map[string]*StoredJob` protected by `sync.RWMutex`. The server is the single source of truth; the sidebar history is rendered server-side and polled every 5s via `hx-get`.

Other options considered:
- localStorage — diverges from HTMX's hypermedia philosophy, requires dual-source syncing, breaks across tabs
- File-based persistence — adds complexity without upside for a tool where in-memory is acceptable (jobs are ephemeral, local files cached on disk already)

Update: each job directory now holds a `job.json` with its StoredJob, and the store is rebuilt from these files at startup. The store is still the only runtime source of truth; the files only carry the history across restarts.
