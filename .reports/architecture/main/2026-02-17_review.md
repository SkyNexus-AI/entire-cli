# In-Flight Review: SkyNexus-AI/entire-cli — 2026-02-17

- **Feature/Area Reviewed:** Entire CLI — main branch baseline assessment
- **Branch:** main
- **Reviewed At:** a0746526bfefcc4042b600d5262c43bd1c4cc9bb
- **Prior Review:** first review
- **Goal Record Reference:** none — NEEDS CREATION
- **Alignment Score:** 5
- **Cumulative Health:** Green

## Dev Context

- **Large files**: `manual_commit_test.go` (3,354 lines), `explain_test.go` (3,578 lines), `checkpoint_test.go` (2,973 lines) — comprehensive test coverage but may be slow to navigate
- **Strategy files are substantial**: `manual_commit_hooks.go` (1,858 lines), `common.go` (1,542 lines) — understand the hook lifecycle before modifying
- **go-git v5 bug**: Do NOT use go-git for `checkout` or `reset --hard` operations — they incorrectly delete gitignored directories. Use git CLI instead (see CLAUDE.md)
- **Repo root vs cwd**: Always use `paths.RepoRoot()` for git-relative paths, not `os.Getwd()` — Claude often runs from subdirectories
- **E2E tests cost money**: Never run E2E tests proactively — they make real API calls to Claude Code
- **Two content-aware checks exist**: Overlap detection (for commit linking) and carry-forward detection (for partial staging) serve different purposes — understand which is relevant before modifying

## Prior Review Checklist

N/A — this is the first review of this branch.

## Observations

### Scope

The project has a clear, focused scope: hook into git workflow to capture AI agent sessions on every push, creating a searchable record of how code was written. The implementation maintains this scope across the codebase:

- **Core commands**: `enable`, `disable`, `rewind`, `resume`, `status`, `explain`, `doctor`, `reset`, `clean` — all directly serve session management
- **Strategy pattern**: Two strategies (`manual-commit`, `auto-commit`) provide flexibility without scope creep — both serve the same core purpose with different tradeoffs
- **Multi-agent support**: Claude Code and Gemini CLI supported — reasonable scope expansion given the product's purpose

Recent additions (v0.4.5) like hook manager detection and content-aware carry-forward directly enhance the core value proposition without introducing unrelated concerns.

### Purpose

The implementation clearly serves the stated goal: "capture AI agent sessions on every push, creating a searchable record of how code was written." Key evidence:

- Checkpoint-to-commit linking provides traceability from code back to the AI conversation that produced it
- Rewind capability allows recovery when an agent "goes sideways"
- Session metadata is kept separate from code commits (on `entire/checkpoints/v1` branch), preserving clean git history
- The `explain` command enables understanding "why" code changed by surfacing the prompt/response transcript

The checkpoint-scenarios.md document demonstrates thoughtful handling of edge cases (stash/unstash, partial staging, concurrent sessions) that stay focused on the core problem.

### Complexity

Complexity is proportional to the problem space:

**Justified complexity:**
- State machine for session phases (ACTIVE, IDLE, ENDED) — necessary for handling commits during vs. between turns
- Content-aware overlap detection — prevents incorrect attribution when user reverts and rewrites
- Shadow branch management with worktree support — required for multi-directory workflows

**Potential concerns:**
- The `manual_commit_hooks.go` at 1,858 lines is the largest non-test file — worth watching but currently manageable given the number of hook scenarios it handles
- 24,969 total lines in `strategy/` package — substantial but well-organized across multiple focused files

The 10-minute explainability test: A new developer could understand what Entire does and why from README.md and CLAUDE.md in that time. The checkpoint-scenarios.md document is excellent for deeper understanding.

### Dependencies

Dependencies are appropriate and expected:
- `go-git/v5` — git operations (with documented workarounds for known bugs)
- `spf13/cobra` — CLI framework standard
- `charmbracelet/huh` — terminal UI forms
- `zricethezav/gitleaks` — secret detection (appropriate given session logs may contain sensitive content)
- `posthog/posthog-go` — optional telemetry

No unexpected dependencies. The project explicitly documents external hook manager compatibility (Husky, Lefthook, Overcommit) which shows awareness of the integration landscape.

### Explainability

Entire CLI hooks into your git workflow to capture what AI agents did during each coding session. It creates searchable checkpoints linked to your commits so you can understand why code changed, recover from mistakes, and maintain traceability — all while keeping your git history clean.

This was easy to write. The project has a clear, communicable purpose.

## Sentinel Cross-Reference

No Sentinel analysis available for this repo.

## Recommended Actions

- **Create a goal record** for this project to establish the alignment baseline for future reviews
- Consider whether the project would benefit from Sentinel integration for automated health tracking
- No architectural concerns requiring immediate attention

## Notes for Next Review

- [ ] Verify goal record has been created
- [ ] Check if `manual_commit_hooks.go` continues to grow — may need refactoring if it exceeds 2,500 lines
- [ ] Monitor test file sizes — the largest test files are approaching 3,500 lines which may impact maintainability
- [ ] Watch for scope expansion into features beyond session capture (e.g., code review, suggestions) — these would be purpose drift
- [ ] Check Gemini CLI support maturity as it moves out of preview
