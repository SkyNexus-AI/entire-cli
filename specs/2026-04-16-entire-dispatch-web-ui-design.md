# `dispatch` web UI on entire.io — Design

**Date:** 2026-04-16
**Status:** Draft — awaiting review
**Companion spec:** [`2026-04-16-entire-dispatch-design.md`](./2026-04-16-entire-dispatch-design.md) (the CLI + backend endpoint)

## Summary

Web UI on entire.io for viewing, browsing, and creating dispatches. Three core pages:

1. **Detail page** (`/dispatches/:id`) — URL-shareable rendered dispatch with metadata sidebar.
2. **Personal feed** (`/dispatches`) — chronological list of dispatches the viewer has access to, with filters.
3. **Create form** (`/dispatches/new`) — web equivalent of the CLI wizard. Calls the same `POST /api/v1/users/me/dispatches` endpoint the CLI uses.

Plus a per-repo index (`/gh/:org/:repo/dispatches`) reusing the same card layout pre-filtered to the repo, consistent with how `checkpoints`, `trails`, and `runners` already live under `_repo/gh/`.

**Repo / codebase** — the entire.io frontend, not the CLI repo. All backend routes and frontend pages for this feature must be developed in a **new** git worktree whose branch is created off the `analysis-chunk-merge` branch (located at `/Users/alisha/Projects/wt/entire.io/analysis-chunk-merge`). Do **not** make changes on the `analysis-chunk-merge` worktree/branch itself — it is used only as the starting point.

## Goals

- Let users view a generated dispatch via a URL (share with teammates, link from CLI `web_url` response).
- Let users browse dispatches they have access to, filtered by repo / date / creator.
- Let users who haven't installed the CLI create a dispatch from the web (same fields as the wizard).
- Blend into the existing entire.io navigation alongside checkpoints, trails, runners.

## Non-goals

- Editing dispatch prose. v1 is read-only; to get different prose, regenerate with different flags.
- Commenting / reactions / social features. Out of scope.
- Public (non-authenticated) dispatch pages. Everything requires login.
- Real-time collaborative editing. N/A (no editing).

## Access control

**Strict** — a user can view a dispatch only if they have access to **every** repo the dispatch covers. For an `--org`-scoped dispatch, this means access to every repo in the org at the time of rendering.

Server enforces access on:

- `GET /api/v1/users/me/dispatches` (list) — returns only dispatches the viewer passes the strict check for.
- `GET /api/v1/dispatches/:id` (detail) — 404 on access failure (not 403 — don't leak existence).
- `POST /api/v1/users/me/dispatches` (create) — creator must have access to all covered repos at creation time. The created dispatch snapshots the access boundary; if the creator later loses access to one of the repos, they still see it (history preservation).

## Routes

| Route | Purpose | Auth |
|---|---|---|
| `/dispatches` | Personal feed — all dispatches viewer has access to | `_authenticated` |
| `/dispatches/new` | Create form | `_authenticated` |
| `/dispatches/:id` | Detail page | `_authenticated` + strict access check |
| `/gh/:org/:repo/dispatches` | Per-repo filtered index | `_authenticated` + repo access check |

Follows the existing TanStack Router conventions in the codebase (`_authenticated/*.tsx`, `_repo/gh/$org.$repo.*.tsx`).

## Pages

### Detail page — `/dispatches/:id`

**Layout**: two-column. Main column renders the dispatch markdown (same payload as `--format markdown` from the CLI). Right sidebar (280px) shows metadata.

**Main column** — rendered markdown:

- Title: `Weekly dispatch — entireio/cli` (or similar; generated or user-provided)
- Meta line under title: date range · branches · checkpoint count · file count
- Body: themed sections with bullets (same as CLI output)
- When generated with a voice preset, voice intro/outro render with a subtle visual treatment (e.g., italic + colored bar) so voice-authored lines are distinguishable from bullet content.

**Sidebar** sections, top to bottom:

- **Creator** — avatar, name, `@handle`, "2h ago" relative time.
- **Scope** — repo badges, window, branches, voice name.
- **Actions** (buttons):
  - `Regenerate` (primary) — reruns the same scope + voice; new dispatch, keeps original. Loading state inline.
  - `Copy markdown` — clipboard copy of the raw markdown payload.
  - `Open in CLI` — opens a modal showing the equivalent `entire dispatch …` command with a Copy button.
  - `Copy share link` — copies the current URL.

**Empty / error states**:

- Dispatch not found or no access → 404 page (same layout as rest of site).
- Dispatch still generating → loading skeleton, poll status every 2s, render when complete.
- Generation failed → error card with reason + "Retry" button.

### Personal feed — `/dispatches`

**Layout**: one-column list.

**Header** — "Dispatches" + filter bar:

- Repo filter (multi-select, default "All repos")
- Date range (preset: Last 7d / Last 30d / All time, or custom)
- Creator filter (default "Anyone"; "Me" / specific user)
- Search input (matches title and body text, via existing search infrastructure if available)
- `+ New dispatch` button (right-aligned, primary) → `/dispatches/new`

**Cards** — one per dispatch:

- Avatar (creator's, colored gradient if no photo)
- Title (first heading of the dispatch or autogenerated from scope)
- Body preview (~180 chars of the rendered text)
- Footer row: repo badges, checkpoint count, voice name
- Date column on the right (relative time + window)
- Click → `/dispatches/:id`

**Empty state**: "No dispatches yet. Generate your first with `entire dispatch` in the CLI, or [create one here](/dispatches/new)."

**Pagination**: infinite scroll or `Load more` button. 20 per page.

### Per-repo index — `/gh/:org/:repo/dispatches`

Same card layout as Personal feed, pre-filtered to the repo. Accessible from the repo sidebar alongside Checkpoints / Trails / Runners.

### Create form — `/dispatches/new`

**Layout**: two-column. Main column is the form, right column (340px) is the live preview.

**Form** (grouped into fieldsets):

- **Scope**
  - Repos / Org toggle (pill selector). Default: repos, pre-filled with the current repo if the user is navigating from `/gh/:org/:repo/dispatches`.
  - Time window — pill row: 24h / 7d (default) / 30d / Custom (opens date picker).
  - Branches — pill row: Default (current) / All / Select… (opens multi-select).
- **Output**
  - Format — pill row: Markdown (default) / Text / JSON.
  - Generate prose? — Bullets only / Generate with voice (default).
  - Voice — card grid: Neutral · Marvin · Custom description · Upload .md. One card selected at a time.

**Preview panel** (right column):

- Live "what this will cover" stats: repos, branches, window, checkpoints count, files touched, unpushed count (if any).
- Equivalent CLI command rendered in a monospace block — updates as fields change. Copy button.

**Buttons**:

- `Generate dispatch` (primary) — submits; shows inline loading, redirects to detail page on completion.
- `Preview (dry-run)` (secondary) — calls the dispatch endpoint with `dry_run: true`, renders the would-be output inline (not persisted, no LLM call).

**Validation**:

- Repo/org field required.
- Custom window requires valid date parsing.
- Voice "Upload .md" requires a file < 50KB of markdown.
- Errors show inline with the relevant field, not a global banner.

## Component architecture

Follows the existing domain-per-feature pattern in `frontend/src/domains/platform/`:

```
frontend/src/domains/platform/dispatches/
  index.ts                            # public exports
  api.ts                              # client for /api/v1/…/dispatches
  api.test.ts
  routeConfig.ts                      # TanStack route config
  breadcrumbs.ts
  breadcrumbs.test.ts
  components/
    DispatchDetail.tsx                # rendered markdown + sidebar container
    DispatchSidebar.tsx               # creator / scope / actions
    DispatchCard.tsx                  # feed card
    DispatchFilterBar.tsx             # filters on feed page
    DispatchForm.tsx                  # create form
    DispatchPreviewPane.tsx           # live preview on create page
    VoicePicker.tsx                   # voice card selector
    RegenerateButton.tsx              # async action button with status
    OpenInCliModal.tsx                # "open in cli" modal w/ copy command
  hooks/
    useDispatch.ts                    # fetch single dispatch + polling if generating
    useDispatchList.ts                # list w/ filters
    useCreateDispatch.ts              # mutation
    useDispatchPreview.ts             # dry-run mutation
  pages/
    DispatchDetailPage.tsx
    DispatchDetailPage.test.tsx
    DispatchFeedPage.tsx
    DispatchFeedPage.test.tsx
    DispatchPerRepoPage.tsx
    DispatchPerRepoPage.test.tsx
    DispatchNewPage.tsx
    DispatchNewPage.test.tsx
```

Routes wire-up:

- `frontend/src/routes/_authenticated/dispatches.index.tsx` → `DispatchFeedPage`
- `frontend/src/routes/_authenticated/dispatches.new.tsx` → `DispatchNewPage`
- `frontend/src/routes/_authenticated/dispatches.$dispatchId.tsx` → `DispatchDetailPage`
- `frontend/src/routes/_repo/gh/$org.$repo.dispatches.index.tsx` → `DispatchPerRepoPage`

Top-level nav (the `topbar`) gains a "Dispatches" entry between "Repos" and "Search" (visible in the mockup). Per-repo nav (`_repo` layout) gains a "Dispatches" entry alongside Checkpoints / Trails / Runners.

## API surface (frontend ↔ server)

All endpoints live under the entire.io backend and are consumed by `api.ts` in the dispatches domain.

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/v1/users/me/dispatches` | `GET` | List dispatches (paginated, filterable). Query params: `repo`, `org`, `since`, `until`, `creator`, `q`, `cursor`, `limit`. |
| `/api/v1/users/me/dispatches` | `POST` | Create dispatch. Body = same as CLI spec (`repo`, `org`, `since`, `branches`, `generate`, `voice`, `dry_run`, `format`). Returns the created dispatch with `id` and `web_url`. |
| `/api/v1/dispatches/:id` | `GET` | Fetch a single dispatch by id. Strict access check; 404 on access failure. |
| `/api/v1/users/me/dispatches/:id/regenerate` | `POST` | Creates a new dispatch with the same scope/voice as `:id`. Returns the new dispatch. Exists because the create endpoint requires full scope resolution; regenerate just passes the original id. |

**Existing endpoints reused**:

- `POST /api/v1/users/me/checkpoints/analyses/batch` — **not used by the web UI directly**. The web UI always goes through `POST /dispatches`, which internally consumes cached analyses server-side.

**New endpoints required** (in addition to what's in the CLI spec):

- `GET /api/v1/users/me/dispatches` — list
- `GET /api/v1/dispatches/:id` — detail
- `POST /api/v1/users/me/dispatches/:id/regenerate` — regenerate

These live in the entire.io backend codebase, not the CLI, and must be built in a **new worktree branched off `analysis-chunk-merge`** (see the Repo / codebase section above for the precise rule).

## Styling and design system

- Reuse existing design tokens (colors, spacing, typography) from the entire.io frontend. No new color palette.
- Existing patterns in `domains/platform/checkpoints/` are a direct reference — the dispatch domain is essentially parallel in structure.
- Markdown rendering: reuse whatever is already used for checkpoint summaries on the checkpoint detail pages (so the "voice intro/outro" block styling is consistent with existing callouts if any).
- Icons: `lucide-react` if it's already in the project (check at impl time); otherwise match whatever the rest of entire.io uses.
- Production polish / aesthetic refinement (padding, type scale, motion, hover states) is delegated to the `frontend-design` skill at implementation time, per the project's CLAUDE.md convention.

## Loading, error, and empty states

| State | Treatment |
|---|---|
| Dispatch generating | Detail page shows a skeleton + "Generating, usually takes 5–15s" message; polls every 2s. |
| Generation failed | Card with error reason, retry button. |
| No dispatches in feed | CTA: "Generate your first with `entire dispatch` in the CLI, or [create one here](/dispatches/new)." |
| No dispatches matching filter | "No dispatches match. Clear filters." |
| 404 (not found / no access) | Standard 404 page. |
| Network error | Toast + retry button. |

## Testing

Following the existing `*.test.tsx` convention in `domains/platform/*/`:

- **Unit tests** — `DispatchCard`, `VoicePicker`, `DispatchPreviewPane`, `DispatchSidebar`. Snapshot and behavior.
- **Page tests** (following `CheckpointsPage.test.tsx` pattern) — render each page with mocked API responses; assert states: loading, loaded, error, empty.
- **API client tests** (`api.test.ts`) — mock fetch, assert request shape for list/detail/create/regenerate.
- **Access control tests** (backend, not frontend) — in the entire.io backend repo: 404 on cross-repo dispatch when viewer lacks access to one repo; allow when viewer has access to all repos.
- **E2E** (if the repo has Playwright or similar) — create dispatch from form, see it in feed, click through to detail. Deferred to post-v1 if not already set up.

## Open questions

1. **Dispatch title generation** — the CLI spec doesn't mandate a title; the web needs one to render in cards/feeds. Options: use the first `<h1>`/`<h2>` in the generated markdown; or fall back to "Dispatch for `<repo>` — `<date-range>`". Decision deferred to implementation — either is fine, but we need to pick one.
2. **Sharing links for external viewers** — currently all dispatches require login. If a shareable "unlisted" mode is added later (per original question 1 option D), the detail route may need a public-friendly variant (`/d/:short-id` or similar). Deferred; out of v1.
3. **Search integration** — the filter-bar text search should probably go through the existing search infrastructure used by `/search`. Whether that's a new `type: dispatch` document or a separate endpoint is a server decision. Deferred to impl.
4. **Regenerate button behavior** — does it create a new dispatch (keeping the old one) or overwrite? Spec'd as "new dispatch" for history preservation. Could revisit.
5. **Nav placement** — "Dispatches" in the top-nav vs in a user menu vs sidebar. Mockup puts it top-nav; refine during impl.
6. **Per-org overview page** — no org-level landing page spec'd. Users going `/dispatches` see all accessible dispatches regardless of org; they can filter. If a dedicated org page is wanted, add `/orgs/:org/dispatches` later.

## Implementation plan

To be produced via the `writing-plans` skill once this spec is approved.
