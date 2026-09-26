# Multi-page HTMX with hx-push-url

We structure the frontend as multiple pages (`/`, `/jobs/:id`) navigated via `hx-push-url` and `hx-target`, keeping the sidebar static while swapping main content. This gives SPA-like UX without client-side routing.

Other options considered:
- Single-page with JS routing — required maintaining URL state manually, lost shareable URLs
- Full page reloads — simpler but janky UX on job submission
