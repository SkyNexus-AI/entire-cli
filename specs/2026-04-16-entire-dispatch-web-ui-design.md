# `dispatch` web UI on entire.io — Design

**Date:** 2026-04-16
**Status:** Draft — awaiting review
**Companion spec:** [`2026-04-16-entire-dispatch-design.md`](./2026-04-16-entire-dispatch-design.md) (the CLI + backend endpoint)

## Summary

Web UI on entire.io for viewing and creating dispatches. Three core pages:

1. **Detail page** (`/dispatches/:id`) — URL-shareable rendered dispatch with metadata sidebar.
2. **Per-repo index** (`/gh/:org/:repo/dispatches`) — list of dispatches whose `covered_repos` includes this repo (and which the viewer has access to in full).
3. **Per-org index** (`/orgs/:org/dispatches`) — list of org-scoped dispatches the viewer has access to.
4. **Create form** (`/dispatches/new`) — web equivalent of the CLI wizard. Calls `POST /api/v1/users/me/dispatches`.

**No personal feed in v1.** Dispatches are content-addressed cache objects with no ownership model — there is no "dispatches I made" list. Users navigate to a repo or org to see dispatches relevant to it. (See CLI spec Persistence and idempotency section for the full content-addressed model.)

**Repo / codebase** — the entire.io frontend, not the CLI repo. All backend routes and frontend pages for this feature must be developed in a **new** git worktree whose branch is created off the `analysis-chunk-merge` branch (located at `/Users/alisha/Projects/wt/entire.io/analysis-chunk-merge`). Do **not** make changes on the `analysis-chunk-merge` worktree/branch itself — it is used only as the starting point. make sure new worktree has codex in the name

## Goals

- Let users view an LLM-generated dispatch via a URL (share with teammates, link from CLI `web_url` response).
- Let users browse dispatches visible under a repo or org.
- Let users who haven't installed the CLI create a dispatch from the web (same fields as the wizard).
- Blend into the existing entire.io navigation alongside checkpoints, trails, runners.

## Non-goals

- Personal dispatch history / "dispatches I made" feed. Deferred — dispatches are shared cache objects in v1.
- DELETE endpoint. Server GCs by age; no user-initiated deletion in v1.
- Editing dispatch prose. v1 is read-only; to get different prose, re-submit Create with different inputs (e.g., different voice).
- Commenting / reactions / social features.
- Public (non-authenticated) dispatch pages. Everything requires login.

## Access control

**Single policy — live strict repo-access check for every viewer.** Follows the CLAUDE.md "most restrictive access controls by default" directive. Dispatches have no creator/owner concept in v1; authorization is a pure function of the viewer's current repo access vs the dispatch's frozen `covered_repos`.

### Authorization algorithm (applied identically across endpoints)

```
authorize(requester, dispatch):
  for repo in dispatch.covered_repos:
    if not requester has current access to repo:
      deny  (respond 404 — don't leak existence)
  allow
```

### Per-endpoint applications

- `GET /api/v1/dispatches/:id` (detail): run `authorize()`; on deny, return 404.
- `GET /api/v1/repos/:org/:repo/dispatches` (per-repo index): return only dispatches whose `covered_repos` includes `:org/:repo` AND whose full `covered_repos` set the viewer currently has access to. Multi-repo dispatches that also cover repos the viewer lacks are excluded from the listing (no leak via title/count).
- `GET /api/v1/orgs/:org/dispatches` (per-org index): same filter with `covered_repos` scoped to repos within `:org`.
- `POST /api/v1/users/me/dispatches` (create): caller must currently have access to every repo in the resolved scope:
  - For `repo: "owner/name"`, verify current access.
  - For `org: "name"`, resolve scope to the intersection of the org's repos and the caller's current access list. Use that filtered set as `covered_repos`. If intersection is empty, return 404 (do not persist empty dispatches).
  - Row stores `covered_repos` (the frozen authorization boundary) and the content. No `creator_id`.
- **No DELETE endpoint in v1.** Server GCs old rows by age (operational retention default: 90 days).

### Required server-side tests

- User A creates a dispatch covering repos X and Y. Another user B has access to both X and Y. User C only has access to X.
  - `GET /dispatches/<id>` as A → allowed (has X and Y).
  - `GET /dispatches/<id>` as B → allowed.
  - `GET /dispatches/<id>` as C → 404 (fails on Y; existence not leaked).
  - User A later loses access to Y: `GET /dispatches/<id>` as A → 404 (content-addressed model = no creator exception, same as any other viewer).
- Mixed-access scenario: user with access to repo A but not repo B; attempts `POST` with `org:` including both → server filters `covered_repos` to `[A]` only, never silently includes B. If A is empty of candidates, returns 404.
- Per-repo index as C on `/gh/:org/X/dispatches`: multi-repo dispatches that also cover Y are not listed. Single-repo dispatches on X are listed.
- Two different users submitting the same inputs get the same cached dispatch record (verify by asserting `POST` from user A and then from user B for identical inputs both return the same `id`).

## Routes

| Route | Purpose | Auth |
|---|---|---|
| `/dispatches/new` | Create form | `_authenticated` |
| `/dispatches/:id` | Detail page | `_authenticated` + `authorize()` (live strict check; 404 on deny) |
| `/gh/:org/:repo/dispatches` | Per-repo index | `_authenticated` + current access to `:org/:repo` + per-dispatch `authorize()` on every listed result |
| `/orgs/:org/dispatches` | Per-org index | `_authenticated` + org membership + per-dispatch `authorize()` on every listed result |

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

- **Scope** — repo badges (from `covered_repos`), window (normalized_since/until), branches, voice name.
- **Created** — `generated_at` timestamp. No creator identity shown (dispatches have no ownership model in v1).
- **Actions** (buttons):
  - `Copy markdown` — clipboard copy of the raw markdown payload.
  - `Open in CLI` — opens a modal showing the equivalent `entire dispatch …` command with a Copy button.
  - `Copy share link` — copies the current URL.

  Note: no Regenerate and no Delete buttons. With the content-addressed fingerprint (see CLI spec), a user who wants to refresh re-submits the Create form (or re-runs the CLI) — if the used checkpoint set has changed, a new dispatch is produced; if not, the existing record is returned.

**Empty / error states**:

- Dispatch not found or no access → 404 page (same layout as rest of site).
- Dispatch still generating → loading skeleton, poll status every 2s, render when complete.
- Generation failed → error card with reason + "Retry" button.

### Per-repo index — `/gh/:org/:repo/dispatches`

**Layout**: one-column list, accessible from the repo sidebar alongside Checkpoints / Trails / Runners.

**Header** — "Dispatches for `:org/:repo`" + filter bar:

- Date range (preset: Last 7d / Last 30d / All time, or custom)
- Voice filter (default "All voices"; neutral / marvin / etc.)
- Search input (matches title and body text, via existing search infrastructure if available)
- `+ New dispatch` button (right-aligned, primary) → `/dispatches/new` with the repo pre-filled

**Cards** — one per dispatch (only those passing the per-dispatch `authorize()` check):

- Title (first heading of the dispatch or autogenerated from scope)
- Body preview (~180 chars of the rendered text)
- Footer row: `covered_repos` badges (if multi-repo), checkpoint count, voice name
- Date column on the right (relative time + window)
- Click → `/dispatches/:id`

**Empty state**: "No dispatches yet for this repo. [Create one here](/dispatches/new)."

**Pagination**: infinite scroll or `Load more` button. 20 per page.

### Per-org index — `/orgs/:org/dispatches`

Same card layout as per-repo index, pre-filtered to dispatches whose `covered_repos` intersects `:org`. Useful when the dispatch was created with `--org=…` (multi-repo).

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

- Live "what this will cover" stats: repos, branches, window, checkpoints count (candidate), used checkpoint count (after fallback chain), files touched.
- Equivalent CLI command rendered in a monospace block — updates as fields change. Copy button.

**Buttons**:

- `Generate dispatch` (primary) — submits; shows inline loading, redirects to detail page on completion.
- `Preview (dry-run)` (secondary) — calls `POST /dispatches` with `dry_run: true`, renders the preview-shaped response inline (no `id`, no `web_url`, no persistence, no LLM call; see CLI spec Idempotency section for the preview schema). Clicking Preview never leaves the Create page.

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
    DispatchSidebar.tsx               # scope + actions (no creator)
    DispatchCard.tsx                  # index card (per-repo / per-org)
    DispatchFilterBar.tsx             # filters on index pages
    DispatchForm.tsx                  # create form
    DispatchPreviewPane.tsx           # live preview on create page
    VoicePicker.tsx                   # voice card selector
    OpenInCliModal.tsx                # "open in cli" modal w/ copy command
  hooks/
    useDispatch.ts                    # fetch single dispatch + polling if generating
    useRepoDispatches.ts              # list for /gh/:org/:repo/dispatches
    useOrgDispatches.ts               # list for /orgs/:org/dispatches
    useCreateDispatch.ts              # mutation (handles generate:true, generate:false, dry_run)
  pages/
    DispatchDetailPage.tsx
    DispatchDetailPage.test.tsx
    DispatchPerRepoPage.tsx
    DispatchPerRepoPage.test.tsx
    DispatchPerOrgPage.tsx
    DispatchPerOrgPage.test.tsx
    DispatchNewPage.tsx
    DispatchNewPage.test.tsx
```

Routes wire-up:

- `frontend/src/routes/_authenticated/dispatches.new.tsx` → `DispatchNewPage`
- `frontend/src/routes/_authenticated/dispatches.$dispatchId.tsx` → `DispatchDetailPage`
- `frontend/src/routes/_repo/gh/$org.$repo.dispatches.index.tsx` → `DispatchPerRepoPage`
- `frontend/src/routes/_authenticated/orgs/$org.dispatches.index.tsx` → `DispatchPerOrgPage`
- No personal-feed route (`/dispatches` root) in v1.

Top-level nav (the `topbar`) gains a "Dispatches" entry between "Repos" and "Search" (visible in the mockup). Per-repo nav (`_repo` layout) gains a "Dispatches" entry alongside Checkpoints / Trails / Runners.

## API surface (frontend ↔ server)

All endpoints live under the entire.io backend and are consumed by `api.ts` in the dispatches domain.

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/v1/users/me/dispatches` | `POST` | Create dispatch (or preview with `dry_run: true`). Body: `{ repo?: "owner/name", org?: "name", since: ISO, until: ISO, branches: string[] \| "all", generate: boolean, voice?: string, dry_run?: boolean }`. Server normalizes `since` (floor-to-minute) and `until` (ceil-to-minute). **Response shape depends on `generate` and `dry_run`**: `generate: true` + `dry_run: false` → persisted dispatch with `id`, `fingerprint_hash`, `web_url`, `status`, `deduped`, covered_repos, generated_text; `generate: false` + `dry_run: false` → inline bullets payload without `id`/`web_url`/`fingerprint_hash`/`status`/`deduped`/`generated_text` (nothing persisted; `"generate": false` present); `dry_run: true` (either generate value) → preview payload with `dry_run: true`, `requested_generate`, same absent-field list, no persistence, no LLM. See CLI spec Persistence-and-idempotency section for the full schema. |
| `/api/v1/dispatches/:id` | `GET` | Fetch a single persisted dispatch. Runs `authorize()` (live strict check — no creator exception, no ownership). Deny returns 404 (no existence leak). Only `generate: true` dispatches have persisted ids to fetch. |
| `/api/v1/repos/:org/:repo/dispatches` | `GET` | List persisted dispatches for a repo. Response items pass `authorize()` individually (viewer must currently have access to every `covered_repo` of each listed dispatch). Query params: `since`, `until`, `voice`, `q`, `cursor`, `limit`. |
| `/api/v1/orgs/:org/dispatches` | `GET` | List persisted dispatches for an org. Same per-item authorize filtering. Query params as above. |

**No DELETE endpoint.** Retention is managed server-side by age (operational default 90 days).

**Existing endpoints reused**:

- `POST /api/v1/users/me/checkpoints/analyses/batch` — **not used by the web UI directly**. The web UI always goes through `POST /dispatches`, which internally consumes cached analyses server-side.

**New endpoints required** (in addition to what's in the CLI spec's `POST /dispatches`):

- `GET /api/v1/dispatches/:id` — detail
- `GET /api/v1/repos/:org/:repo/dispatches` — per-repo index
- `GET /api/v1/orgs/:org/dispatches` — per-org index

**Content-addressed idempotency** — `POST /dispatches` with `generate: true` dedupes on `sha256(sha256(lex_sorted(used_checkpoint_ids)) + "|" + normalized_voice)` only. No user, scope, window, branches, or `generate` flag in the key. Two users submitting the same inputs get the same cached dispatch (shared cache). `generate: false` and `dry_run: true` never persist and are not dedupe-checked. See CLI spec Persistence-and-idempotency section for the full contract including the atomic reserve-before-synthesize SQL pattern and failure-sweeper behavior.

There is no `regenerate` endpoint — re-submitting the Create form is the refresh path. No DELETE — server GC by age handles cleanup.

These endpoints live in the entire.io backend codebase, not the CLI, and must be built in a **new worktree branched off `analysis-chunk-merge`** (see the Repo / codebase section above for the precise rule).

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
| No dispatches for this repo/org | CTA: "Generate your first with `entire dispatch --generate` in the CLI, or [create one here](/dispatches/new)." |
| No dispatches matching filter | "No dispatches match. Clear filters." |
| 404 (not found / no access) | Standard 404 page. |
| Network error | Toast + retry button. |

## Testing

Following the existing `*.test.tsx` convention in `domains/platform/*/`:

- **Unit tests** — `DispatchCard`, `VoicePicker`, `DispatchPreviewPane`, `DispatchSidebar`. Snapshot and behavior.
- **Page tests** (following `CheckpointsPage.test.tsx` pattern) — render each page with mocked API responses; assert states: loading, loaded, error, empty.
- **API client tests** (`api.test.ts`) — mock fetch, assert request shape for `POST /dispatches` (all four combinations of `generate` × `dry_run`), `GET /dispatches/:id`, per-repo and per-org listings.
- **Access control tests** (backend, not frontend) — 404 on per-dispatch authorize failure, including the A-loses-Y scenario in this file's Required server-side tests section; mixed-access org create filters; per-repo/per-org listings exclude dispatches whose full `covered_repos` the viewer lacks.
- **Content-addressed fingerprint tests** (backend): same used-ID set + same voice → `deduped: true` regardless of caller/window/scope/branches; pending→complete joining used set → new id; used checkpoint revoked/failed → new id; different voice → new id; concurrent `generate: true` submits with same fingerprint → reserve-before-synthesize guarantees exactly one record AND exactly one LLM invocation; reservation claimant crashes → sweeper marks row failed, retry succeeds; two different users submitting identical inputs return the same `id`; `generate: false` and `dry_run: true` never insert a dispatches row.
- **Response-shape tests** (backend + frontend): for each of the four `generate × dry_run` combos, assert the documented response shape (bullets-only + dry-run explicitly lack `id`/`web_url`/`fingerprint_hash`/`status`/`deduped`/`generated_text`; only `generate:true + dry_run:false` includes them). Frontend client never navigates to `/dispatches/:id` or stores an `id` unless the response actually contains one.
- **E2E** (if the repo has Playwright or similar) — create dispatch from form, see it appear in the per-repo index, click through to detail. Deferred to post-v1 if not already set up.

## Open questions

1. **Dispatch title generation** — the CLI spec doesn't mandate a title; the web needs one to render in cards/feeds. Options: use the first `<h1>`/`<h2>` in the generated markdown; or fall back to "Dispatch for `<repo>` — `<date-range>`". Decision deferred to implementation — either is fine, but we need to pick one.
2. **Sharing links for external viewers** — currently all dispatches require login. A shareable "unlisted" mode would need a public-friendly variant route and a security review. Deferred; out of v1.
3. **Search integration** — the filter-bar text search should probably go through the existing search infrastructure used by `/search`. Whether that's a new `type: dispatch` document or a separate endpoint is a server decision. Deferred to impl.
4. **Explicit LLM-reshuffle affordance** — no longer in v1. If users report wanting a "same inputs + same data, but different wording" button, add a caller-scoped bypass-dedupe flag to `POST /dispatches`. Current design handles refreshes via re-submission; reshuffle has a workaround (tweak the voice).
5. **Nav placement** — "Dispatches" in the top-nav vs in a user menu vs sidebar. Mockup puts it top-nav; refine during impl.
6. **Personal dispatch history** — v1 has no "dispatches I made" view. If users ask for it, add a lightweight server-side submissions log (many-to-many between user and dispatch) in a later version. Non-breaking to add.
7. **DELETE** — deferred. Server GC by age is the retention mechanism. If a concrete need emerges (e.g. sensitive content accidentally included), add a policy-reviewed admin-only DELETE at that time.

## Implementation plan

To be produced via the `writing-plans` skill once this spec is approved.
