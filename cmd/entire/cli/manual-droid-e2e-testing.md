# Manual E2E Testing: Factory AI Droid (Interactive Mode)

This guide translates every automated E2E test from `cmd/entire/cli/e2e_test/` into step-by-step instructions for manual testing with Factory AI Droid in **interactive** mode. The automated tests run agents in non-interactive/exec mode; this guide validates behavior when a human operates Droid interactively with real hooks firing.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Common Setup](#common-setup)
- [Basic Workflow Tests](#basic-workflow-tests)
  - [Test 1: BasicWorkflow](#test-1-basicworkflow)
  - [Test 2: MultipleChanges](#test-2-multiplechanges)
- [Checkpoint Tests](#checkpoint-tests)
  - [Test 3: CheckpointMetadata](#test-3-checkpointmetadata)
  - [Test 4: CheckpointIDFormat](#test-4-checkpointidformat)
  - [Test 5: AutoCommitStrategy](#test-5-autocommitstrategy)
- [Agent Commits Tests](#agent-commits-tests)
  - [Test 6: AgentCommitsDuringTurn](#test-6-agentcommitsduringturn)
  - [Test 7: MultipleAgentSessions](#test-7-multipleagentsessions)
- [Rewind Tests](#rewind-tests)
  - [Test 8: RewindToCheckpoint](#test-8-rewindtocheckpoint)
  - [Test 9: RewindAfterCommit](#test-9-rewindaftercommit)
  - [Test 10: RewindMultipleFiles](#test-10-rewindmultiplefiles)
- [Subagent Tests](#subagent-tests)
  - [Test 11: SubagentCheckpoint](#test-11-subagentcheckpoint)
  - [Test 12: SubagentCheckpoint_CommitFlow](#test-12-subagentcheckpoint_commitflow)
- [Checkpoint Workflow Scenarios](#checkpoint-workflow-scenarios)
  - [Test 13: Scenario 1 – Basic Flow](#test-13-scenario-1--basic-flow)
  - [Test 14: Scenario 2 – Agent Commits During Turn](#test-14-scenario-2--agent-commits-during-turn)
  - [Test 15: Scenario 3 – Multiple Granular Commits](#test-15-scenario-3--multiple-granular-commits)
  - [Test 16: Scenario 4 – User Splits Commits](#test-16-scenario-4--user-splits-commits)
  - [Test 17: Scenario 5 – Partial Commit + Stash + Next Prompt](#test-17-scenario-5--partial-commit--stash--next-prompt)
  - [Test 18: Scenario 6 – Stash + Second Prompt + Unstash + Commit All](#test-18-scenario-6--stash--second-prompt--unstash--commit-all)
  - [Test 19: Scenario 7 – Partial Staging (Simulated)](#test-19-scenario-7--partial-staging-simulated)
- [Content-Aware Detection Tests](#content-aware-detection-tests)
  - [Test 20: ContentAwareOverlap_RevertAndReplace](#test-20-contentawareoverlap_revertandreplace)
- [Existing Files Tests](#existing-files-tests)
  - [Test 21: ExistingFiles_ModifyAndCommit](#test-21-existingfiles_modifyandcommit)
  - [Test 22: ExistingFiles_StashModifications](#test-22-existingfiles_stashmodifications)
  - [Test 23: ExistingFiles_SplitCommits](#test-23-existingfiles_splitcommits)
  - [Test 24: ExistingFiles_RevertModification](#test-24-existingfiles_revertmodification)
  - [Test 25: ExistingFiles_MixedNewAndModified](#test-25-existingfiles_mixednewandmodified)
- [Session Lifecycle Tests](#session-lifecycle-tests)
  - [Test 26: EndedSession_UserCommitsAfterExit](#test-26-endedsession_usercommitsafterexit)
  - [Test 27: DeletedFiles_CommitDeletion](#test-27-deletedfiles_commitdeletion)
  - [Test 28: AgentCommitsMidTurn_UserCommitsRemainder](#test-28-agentcommitsmidturn_usercommitsremainder)
  - [Test 29: TrailerRemoval_SkipsCondensation](#test-29-trailerremoval_skipscondensation)
  - [Test 30: SessionDepleted_ManualEditNoCheckpoint](#test-30-sessiondepleted_manualeditnocheckpoint)
- [Resume Tests](#resume-tests)
  - [Test 31: ResumeInRelocatedRepo](#test-31-resumeinrelocatedrepo)

---

## Prerequisites

1. **Entire CLI** built and in your `$PATH`:
   ```bash
   cd /path/to/cli-repo
   go build -o ~/go/bin/entire ./cmd/entire
   ```

2. **Factory AI Droid** installed with ANTHROPIC_API_KEY set:
   ```bash
   droid --version           # Verify installed
   echo $ANTHROPIC_API_KEY   # Must be set
   ```

3. **Git** configured with a user name and email (for commits):
   ```bash
   git config --global user.name "Test User"
   git config --global user.email "test@example.com"
   ```

4. **jq** installed (for inspecting JSON output):
   ```bash
   jq --version
   ```

---

## Common Setup

Every test starts from a clean test repository. Run these steps before each test (or use the helper script at the bottom).

```bash
# Create a fresh test repo
TEST_DIR=$(mktemp -d)
cd "$TEST_DIR"
git init
git commit --allow-empty -m "Initial commit"
git checkout -b feature/manual-test

# Enable entire with droid agent (default: manual-commit strategy)
entire enable --agent factoryai-droid --strategy manual-commit --telemetry=false --force

# Commit the config files so they survive stash operations
git add .
git commit -m "Add entire and agent config"
```

For **auto-commit strategy** tests, replace `--strategy manual-commit` with `--strategy auto-commit`.

### Starting Droid Interactively

```bash
# Launch droid in interactive mode (in the test repo)
droid
```

When Droid starts, entire's hooks fire via the `.factory/settings.json` configuration. You type prompts directly in Droid's interactive session.

### Verification Commands Reference

These commands are used throughout the tests for verification:

| Command | Purpose |
|---------|---------|
| `entire rewind --list` | List available rewind points (JSON) |
| `entire rewind --list \| jq .` | Pretty-print rewind points |
| `entire rewind --to <ID>` | Rewind to a specific checkpoint |
| `git log --oneline` | Check commit history |
| `git log -1 --format=%B` | Show full message of latest commit |
| `git log --format=%B \| grep "Entire-Checkpoint:"` | Find checkpoint trailers |
| `git branch -a \| grep entire` | List entire-related branches |
| `git show entire/checkpoints/v1:<path>` | Read metadata from checkpoint branch |
| `git status` | Check working tree status |

---

## Basic Workflow Tests

### Test 1: BasicWorkflow

**What it validates:** The fundamental workflow — agent creates a file, user commits, checkpoint is created.

**Corresponds to:** `TestE2E_BasicWorkflow`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid** and type this prompt:
   ```
   Create a file called hello.go with a simple Go program that prints "Hello, World!".
   Use package main, a main function, and fmt.Println. No comments, tests, or extra files.
   ```

3. **Wait for Droid to finish**, then exit Droid (Ctrl+C or `/exit`).

4. **Verify the file was created:**
   ```bash
   cat hello.go
   # Should contain: package main, func main(), fmt.Println("Hello, World!")
   ```

5. **Check rewind points exist:**
   ```bash
   entire rewind --list | jq .
   # Should have at least 1 rewind point
   ```

6. **Commit the file with hooks:**
   ```bash
   git add hello.go
   git commit -m "Add hello world program"
   ```
   The prepare-commit-msg hook should add an `Entire-Checkpoint` trailer.

7. **Verify checkpoint trailer:**
   ```bash
   git log -1 --format=%B | grep "Entire-Checkpoint:"
   # Should print: Entire-Checkpoint: <12-hex-char ID>
   ```

8. **Verify metadata branch exists:**
   ```bash
   git branch -a | grep "entire/checkpoints/v1"
   # Should show the branch
   ```

#### Expected Outcome
- `hello.go` exists with a valid Hello World program
- At least 1 rewind point before commit
- Commit has `Entire-Checkpoint` trailer with 12-hex-char ID
- `entire/checkpoints/v1` branch exists

---

### Test 2: MultipleChanges

**What it validates:** Multiple agent changes across separate prompts before a single commit.

**Corresponds to:** `TestE2E_MultipleChanges`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid** and type:
   ```
   Create a file called hello.go with a simple Go program that prints "Hello, World!".
   Use package main, a main function, and fmt.Println. No comments, tests, or extra files.
   ```

3. **After Droid finishes the first prompt**, type a second prompt:
   ```
   Create a file called calc.go with two exported functions:
   Add(a, b int) int - returns a + b
   Subtract(a, b int) int - returns a - b
   Use package main. No comments, no main function, no tests, no other files.
   ```

4. **Exit Droid**, then verify both files:
   ```bash
   ls hello.go calc.go
   ```

5. **Check rewind points:**
   ```bash
   entire rewind --list | jq 'length'
   # Should be at least 2
   ```

6. **Commit both files:**
   ```bash
   git add hello.go calc.go
   git commit -m "Add hello world and calculator"
   ```

7. **Verify checkpoint:**
   ```bash
   git log -1 --format=%B | grep "Entire-Checkpoint:"
   ```

#### Expected Outcome
- Both `hello.go` and `calc.go` exist
- At least 2 rewind points
- Commit has checkpoint trailer

---

## Checkpoint Tests

### Test 3: CheckpointMetadata

**What it validates:** Checkpoint metadata is correctly stored and accessible.

**Corresponds to:** `TestE2E_CheckpointMetadata`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid** and type:
   ```
   Create a file called config.json with this exact content:
   {"name": "e2e-test", "version": "1.0.0", "enabled": true}
   Do not create any other files.
   ```

3. **Exit Droid**, then check rewind points:
   ```bash
   entire rewind --list | jq '.[0] | {id, metadata_dir, message}'
   # Each point should have an id and metadata_dir
   ```

4. **Commit:**
   ```bash
   git add config.json
   git commit -m "Add config file"
   ```

5. **Extract checkpoint ID:**
   ```bash
   CPID=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   echo "Checkpoint ID: $CPID"
   ```

6. **Verify metadata on checkpoint branch:**
   ```bash
   # Compute sharded path: first 2 chars / remaining chars
   SHARD="${CPID:0:2}/${CPID:2}"
   git show "entire/checkpoints/v1:${SHARD}/metadata.json" | jq .
   # Should contain: checkpoint_id, strategy, sessions, files_touched
   ```

7. **Verify session metadata:**
   ```bash
   git show "entire/checkpoints/v1:${SHARD}/0/metadata.json" | jq .
   # Should contain: checkpoint_id, created_at
   ```

8. **Check post-commit rewind points:**
   ```bash
   entire rewind --list | jq '.[] | {id, is_logs_only, condensation_id}'
   # Should show logs-only points after commit
   ```

#### Expected Outcome
- Rewind points have `metadata_dir` set
- Checkpoint metadata on `entire/checkpoints/v1` contains `checkpoint_id`, `strategy`, `files_touched`
- Session subfolder `0/` contains `metadata.json` with `created_at`
- Post-commit points are marked `is_logs_only: true`

---

### Test 4: CheckpointIDFormat

**What it validates:** Checkpoint IDs are exactly 12 lowercase hex characters.

**Corresponds to:** `TestE2E_CheckpointIDFormat`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid**, create `hello.go` (any simple Go program), exit Droid.

3. **Commit:**
   ```bash
   git add hello.go
   git commit -m "Add hello world"
   ```

4. **Validate checkpoint ID format:**
   ```bash
   CPID=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   echo "Checkpoint ID: '$CPID'"
   echo "Length: ${#CPID}"
   echo "$CPID" | grep -qE '^[0-9a-f]{12}$' && echo "PASS: Valid format" || echo "FAIL: Invalid format"
   ```

#### Expected Outcome
- Checkpoint ID is exactly 12 characters
- Only contains lowercase hex characters (`0-9`, `a-f`)

---

### Test 5: AutoCommitStrategy

**What it validates:** Auto-commit strategy creates commits automatically when Droid finishes.

**Corresponds to:** `TestE2E_AutoCommitStrategy`

#### Steps

1. [Common Setup](#common-setup) but with **auto-commit** strategy:
   ```bash
   entire enable --agent factoryai-droid --strategy auto-commit --telemetry=false --force
   ```

2. **Count commits before:**
   ```bash
   git log --oneline | wc -l
   ```

3. **Start Droid** and type:
   ```
   Create a file called hello.go with a simple Go program that prints "Hello, World!".
   Use package main, a main function, and fmt.Println. No comments, tests, or extra files.
   ```

4. **Exit Droid**, then count commits after:
   ```bash
   git log --oneline | wc -l
   # Should be more than before
   ```

5. **Verify checkpoint in commit:**
   ```bash
   CPID=$(git log --format=%B | grep "Entire-Checkpoint:" | head -1 | awk '{print $2}')
   echo "Checkpoint ID: $CPID"
   echo ${#CPID}  # Should be 12
   ```

6. **Verify metadata branch and rewind points:**
   ```bash
   git branch -a | grep "entire/checkpoints/v1"
   entire rewind --list | jq 'length'
   ```

#### Expected Outcome
- Commit count increased (auto-commit created commits)
- Checkpoint trailer present with 12-hex-char ID
- `entire/checkpoints/v1` branch exists
- At least 1 rewind point

---

## Agent Commits Tests

### Test 6: AgentCommitsDuringTurn

**What it validates:** Behavior when the agent commits during its turn (deferred finalization).

**Corresponds to:** `TestE2E_AgentCommitsDuringTurn`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid** and type:
   ```
   Create a file called hello.go with a simple Go program that prints "Hello, World!".
   Use package main, a main function, and fmt.Println. No comments, tests, or extra files.
   ```

3. **After Droid finishes**, type a second prompt telling Droid to commit:
   ```
   Stage and commit the hello.go file with commit message "Add hello world via agent".
   Use these exact commands:
   1. git add hello.go
   2. git commit -m "Add hello world via agent"
   Only run these two commands, nothing else.
   ```

4. **After Droid finishes the commit**, verify it was made:
   ```bash
   git log -1 --format="%s"
   # Should show the commit message
   ```

5. **Check rewind points:**
   ```bash
   entire rewind --list | jq 'length'
   ```

6. **Still in the same Droid session**, type another prompt:
   ```
   Create a file called calc.go with two exported functions:
   Add(a, b int) int - returns a + b
   Subtract(a, b int) int - returns a - b
   Use package main. No comments, no main function, no tests, no other files.
   ```

7. **Exit Droid**, then commit the second file:
   ```bash
   git add calc.go
   git commit -m "Add calculator"
   ```

8. **Check checkpoint in latest commit:**
   ```bash
   git log -1 --format=%B | grep "Entire-Checkpoint:"
   ```

#### Expected Outcome
- Agent-initiated commit is made during the turn
- Rewind points exist after agent commit
- User's subsequent commit gets checkpoint trailer
- Both files exist (`hello.go`, `calc.go`)

---

### Test 7: MultipleAgentSessions

**What it validates:** Behavior across multiple separate agent sessions with commits between them.

**Corresponds to:** `TestE2E_MultipleAgentSessions`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Session 1:** Start Droid, create `hello.go`, exit Droid.
   ```bash
   # In Droid:
   # Create a file called hello.go with a simple Go program that prints "Hello, World!".
   ```
   ```bash
   git add hello.go && git commit -m "Session 1: Add hello world"
   ```

3. **Session 2:** Start Droid again, create `calc.go`, exit Droid.
   ```bash
   # In Droid:
   # Create calc.go with Add(a, b int) int and Subtract(a, b int) int functions.
   ```
   ```bash
   git add calc.go && git commit -m "Session 2: Add calculator"
   ```

4. **Session 3:** Start Droid again, add Multiply to `calc.go`, exit Droid.
   ```bash
   # In Droid:
   # Add a Multiply function to calc.go: Multiply(a, b int) int
   ```
   ```bash
   git add calc.go && git commit -m "Session 3: Add multiply function"
   ```

5. **Verify all checkpoint IDs are present and unique:**
   ```bash
   git log --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}'
   # Should show 3 different checkpoint IDs
   ```

#### Expected Outcome
- Three separate commits, each with unique checkpoint IDs
- `calc.go` contains `Add`, `Subtract`, and `Multiply` functions
- Each session creates and condenses its own checkpoints

---

## Rewind Tests

### Test 8: RewindToCheckpoint

**What it validates:** Rewinding to a previous checkpoint restores file content.

**Corresponds to:** `TestE2E_RewindToCheckpoint`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid** and create `hello.go`:
   ```
   Create a file called hello.go with a simple Go program that prints "Hello, World!".
   ```

3. **Save the first checkpoint ID:**
   ```bash
   # While still in Droid or after it runs, check rewind points in another terminal:
   FIRST_ID=$(entire rewind --list | jq -r '.[0].id')
   echo "First checkpoint: $FIRST_ID"
   ```

4. **Save the original content:**
   ```bash
   cat hello.go  # Note the content
   ```

5. **In Droid, modify the file:**
   ```
   Modify hello.go to print "Hello, E2E Test!" instead of "Hello, World!".
   Do not add any other functionality or files.
   ```

6. **Verify content changed:**
   ```bash
   cat hello.go  # Should now contain "E2E Test"
   ```

7. **Exit Droid**, then verify we have at least 2 rewind points:
   ```bash
   entire rewind --list | jq 'length'
   ```

8. **Rewind to the first checkpoint:**
   ```bash
   entire rewind --to "$FIRST_ID"
   ```

9. **Verify content was restored:**
   ```bash
   cat hello.go  # Should be back to "Hello, World!"
   grep -q "E2E Test" hello.go && echo "FAIL" || echo "PASS: Content restored"
   ```

#### Expected Outcome
- After rewind, `hello.go` contains the original "Hello, World!" content
- The "E2E Test" modification is gone

---

### Test 9: RewindAfterCommit

**What it validates:** Pre-commit checkpoint IDs become invalid after commit (shadow branch is deleted).

**Corresponds to:** `TestE2E_RewindAfterCommit`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid**, create `hello.go`, exit Droid.

3. **Record the pre-commit rewind point:**
   ```bash
   PRE_ID=$(entire rewind --list | jq -r '.[0].id')
   IS_LOGS_ONLY=$(entire rewind --list | jq -r '.[0].is_logs_only')
   echo "Pre-commit ID: $PRE_ID (is_logs_only: $IS_LOGS_ONLY)"
   # is_logs_only should be false (it's on the shadow branch)
   ```

4. **Commit (triggers condensation and shadow branch deletion):**
   ```bash
   git add hello.go
   git commit -m "Add hello world"
   ```

5. **Check post-commit rewind points:**
   ```bash
   entire rewind --list | jq '.[] | {id, is_logs_only, condensation_id}'
   # Should show logs-only point(s) with DIFFERENT IDs than pre-commit
   ```

6. **Attempt rewind to the OLD pre-commit ID:**
   ```bash
   entire rewind --to "$PRE_ID" 2>&1
   # Should FAIL with "not found" error
   ```

#### Expected Outcome
- Pre-commit checkpoint is NOT logs-only
- Post-commit checkpoints have different IDs and ARE logs-only
- Rewind to old shadow branch ID fails with "not found"

---

### Test 10: RewindMultipleFiles

**What it validates:** Rewinding restores/removes files across multiple file changes.

**Corresponds to:** `TestE2E_RewindMultipleFiles`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid** and create the first file:
   ```
   Create a file called hello.go with a simple Go program that prints "Hello, World!".
   ```

3. **Record the checkpoint after the first file:**
   ```bash
   AFTER_FIRST=$(entire rewind --list | jq -r '.[0].id')
   echo "After first file: $AFTER_FIRST"
   ```

4. **In Droid, create the second file:**
   ```
   Create a file called calc.go with Add(a, b int) int and Subtract(a, b int) int functions.
   Use package main. No comments, no main, no tests.
   ```

5. **Exit Droid and verify both files exist:**
   ```bash
   ls hello.go calc.go
   ```

6. **Rewind to after first file (before second):**
   ```bash
   entire rewind --to "$AFTER_FIRST"
   ```

7. **Verify only the first file exists:**
   ```bash
   ls hello.go && echo "PASS: hello.go exists"
   ls calc.go 2>/dev/null && echo "FAIL: calc.go should not exist" || echo "PASS: calc.go removed"
   ```

#### Expected Outcome
- `hello.go` still exists after rewind
- `calc.go` is removed by the rewind

---

## Subagent Tests

> **Note:** These tests are Claude Code-specific (Task tool). For Droid, adapt them to test whether Droid's subagent/tool usage creates task checkpoints. If Droid does not support a Task tool equivalent, these tests verify that regular checkpoints are still created.

### Test 11: SubagentCheckpoint

**What it validates:** Subagent/task checkpoint creation when Droid delegates work.

**Corresponds to:** `TestE2E_SubagentCheckpoint`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid** and type a prompt that may trigger subagent usage:
   ```
   Create a file called subagent_output.txt containing the text "Created by subagent".
   ```

3. **Exit Droid** and check results:
   ```bash
   cat subagent_output.txt 2>/dev/null || echo "File not created"
   ```

4. **Check rewind points:**
   ```bash
   entire rewind --list | jq '.[] | {id, is_task_checkpoint, tool_use_id, message}'
   ```

5. **Look for task checkpoints (if any):**
   ```bash
   entire rewind --list | jq '[.[] | select(.is_task_checkpoint == true)] | length'
   ```

#### Expected Outcome
- At least one checkpoint exists (task or regular)
- If Droid used a subagent, `is_task_checkpoint: true` points may appear
- If not, regular checkpoints should still exist

---

### Test 12: SubagentCheckpoint_CommitFlow

**What it validates:** Task checkpoints are properly handled through the commit flow.

**Corresponds to:** `TestE2E_SubagentCheckpoint_CommitFlow`

#### Steps

1. Follow [Test 11](#test-11-subagentcheckpoint) steps 1-4.

2. **If a file was created, commit it:**
   ```bash
   git add subagent_output.txt
   git commit -m "Add subagent output"
   ```

3. **Verify checkpoint:**
   ```bash
   CPID=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   echo "Checkpoint ID: $CPID"
   ```

4. **Validate checkpoint on metadata branch:**
   ```bash
   SHARD="${CPID:0:2}/${CPID:2}"
   git show "entire/checkpoints/v1:${SHARD}/metadata.json" | jq .
   ```

5. **Verify logs-only point after commit:**
   ```bash
   entire rewind --list | jq '.[] | select(.is_logs_only == true)'
   ```

#### Expected Outcome
- Commit has checkpoint trailer
- Metadata exists on `entire/checkpoints/v1`
- Post-commit shows logs-only rewind point

---

## Checkpoint Workflow Scenarios

These tests correspond to the scenarios documented in `docs/architecture/checkpoint-scenarios.md`.

### Test 13: Scenario 1 – Basic Flow

**What it validates:** The simplest documented workflow: Prompt → Changes → Prompt Finishes → User Commits.

**Corresponds to:** `TestE2E_Scenario1_BasicFlow`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid** and type:
   ```
   Create a file called scenario1.go with this content:
   package main
   func Scenario1() {}
   Create only this file.
   ```

3. **Exit Droid**, then verify:
   ```bash
   cat scenario1.go
   entire rewind --list | jq 'length'  # At least 1
   ```

4. **Commit:**
   ```bash
   git add scenario1.go
   git commit -m "Add scenario1 file"
   ```

5. **Verify checkpoint and metadata:**
   ```bash
   CPID=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   SHARD="${CPID:0:2}/${CPID:2}"

   # Verify metadata
   git show "entire/checkpoints/v1:${SHARD}/metadata.json" | jq '{
     checkpoint_id, strategy, files_touched
   }'
   # files_touched should include "scenario1.go"
   # strategy should be "manual-commit"

   # Verify transcript exists
   git show "entire/checkpoints/v1:${SHARD}/0/full.jsonl" | head -1 | jq . >/dev/null && echo "PASS: Valid JSONL"
   ```

6. **Verify shadow branch was cleaned up:**
   ```bash
   git branch -a | grep "entire/" | grep -v "checkpoints"
   # Should be empty (no shadow branches remain)
   ```

#### Expected Outcome
- Checkpoint links to metadata with `files_touched: ["scenario1.go"]`
- Transcript exists and is valid JSONL
- No shadow branches remain after condensation

---

### Test 14: Scenario 2 – Agent Commits During Turn

**What it validates:** Deferred finalization when agent commits during ACTIVE phase.

**Corresponds to:** `TestE2E_Scenario2_AgentCommitsDuringTurn`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Record commit count:**
   ```bash
   git log --oneline | wc -l
   ```

3. **Start Droid** and type:
   ```
   Create a file called agent_commit.go with this content:
   package main
   func AgentCommit() {}

   Then commit it with: git add agent_commit.go && git commit -m "Agent adds file"

   Create the file first, then run the git commands.
   ```

4. **Exit Droid**, then verify:
   ```bash
   cat agent_commit.go
   git log --oneline | wc -l  # Should be more than before
   git log -1 --format="%s"   # Check commit message
   ```

5. **Check for checkpoint trailer:**
   ```bash
   git log --format=%B | grep "Entire-Checkpoint:" | head -1
   ```

6. **If checkpoint exists, validate metadata:**
   ```bash
   CPID=$(git log --format=%B | grep "Entire-Checkpoint:" | head -1 | awk '{print $2}')
   if [ -n "$CPID" ]; then
     SHARD="${CPID:0:2}/${CPID:2}"
     git show "entire/checkpoints/v1:${SHARD}/metadata.json" | jq '{files_touched}'
     # Should include "agent_commit.go"
   fi
   ```

#### Expected Outcome
- Agent's commit is present in history
- Checkpoint trailer added (via deferred finalization)
- Metadata correctly references `agent_commit.go`

---

### Test 15: Scenario 3 – Multiple Granular Commits

**What it validates:** Agent making multiple granular commits in a single turn; each gets a unique checkpoint ID.

**Corresponds to:** `TestE2E_Scenario3_MultipleGranularCommits`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Record commit count:**
   ```bash
   BEFORE=$(git log --oneline | wc -l)
   ```

3. **Start Droid** and type:
   ```
   Please do the following tasks, committing after each one:

   1. Create a file called file1.go with this content:
      package main
      func One() int { return 1 }
      Then run: git add file1.go && git commit -m "Add file1"

   2. Create a file called file2.go with this content:
      package main
      func Two() int { return 2 }
      Then run: git add file2.go && git commit -m "Add file2"

   3. Create a file called file3.go with this content:
      package main
      func Three() int { return 3 }
      Then run: git add file3.go && git commit -m "Add file3"

   Do each task in order, making the commit after each file creation.
   ```

4. **Exit Droid**, then verify:
   ```bash
   ls file1.go file2.go file3.go  # All should exist

   AFTER=$(git log --oneline | wc -l)
   echo "New commits: $((AFTER - BEFORE))"  # Should be at least 3
   ```

5. **Verify each commit has a unique checkpoint ID:**
   ```bash
   git log --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}' | sort -u
   # Should show 3 unique IDs
   ```

6. **Verify no stale shadow branches:**
   ```bash
   git branch -a | grep "entire/" | grep -v "checkpoints"
   # Should be empty
   ```

#### Expected Outcome
- 3 new commits, each with a unique checkpoint ID
- All three files exist
- No shadow branches remain (all condensed)

---

### Test 16: Scenario 4 – User Splits Commits

**What it validates:** User splitting agent changes across multiple commits, each getting its own checkpoint.

**Corresponds to:** `TestE2E_Scenario4_UserSplitsCommits`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid** and type:
   ```
   Create these files:
   1. fileA.go with content: package main; func A() string { return "A" }
   2. fileB.go with content: package main; func B() string { return "B" }
   3. fileC.go with content: package main; func C() string { return "C" }
   4. fileD.go with content: package main; func D() string { return "D" }
   Create all four files, no other files or actions.
   ```

3. **Exit Droid**, then verify all files exist:
   ```bash
   ls fileA.go fileB.go fileC.go fileD.go
   ```

4. **Commit only A and B first:**
   ```bash
   git add fileA.go fileB.go
   git commit -m "Add files A and B"
   CPID_AB=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   echo "Checkpoint A,B: $CPID_AB"
   ```

5. **Commit C and D:**
   ```bash
   git add fileC.go fileD.go
   git commit -m "Add files C and D"
   CPID_CD=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   echo "Checkpoint C,D: $CPID_CD"
   ```

6. **Verify unique checkpoint IDs:**
   ```bash
   [ "$CPID_AB" != "$CPID_CD" ] && echo "PASS: Unique IDs" || echo "FAIL: Same ID"
   ```

7. **Validate metadata for each checkpoint:**
   ```bash
   # First checkpoint (A, B)
   SHARD="${CPID_AB:0:2}/${CPID_AB:2}"
   git show "entire/checkpoints/v1:${SHARD}/metadata.json" | jq '.files_touched'
   # Should contain ["fileA.go", "fileB.go"]

   # Second checkpoint (C, D)
   SHARD="${CPID_CD:0:2}/${CPID_CD:2}"
   git show "entire/checkpoints/v1:${SHARD}/metadata.json" | jq '.files_touched'
   # Should contain ["fileC.go", "fileD.go"]
   ```

8. **Verify no shadow branches remain:**
   ```bash
   git branch -a | grep "entire/" | grep -v "checkpoints"
   # Should be empty
   ```

#### Expected Outcome
- Two commits with unique checkpoint IDs
- First checkpoint: `files_touched` = `["fileA.go", "fileB.go"]`
- Second checkpoint: `files_touched` = `["fileC.go", "fileD.go"]`
- No shadow branches remain

---

### Test 17: Scenario 5 – Partial Commit + Stash + Next Prompt

**What it validates:** Partial commit, stash, new prompt with new files, commit new files.

**Corresponds to:** `TestE2E_Scenario5_PartialCommitStashNextPrompt`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid (Prompt 1):**
   ```
   Create these files:
   1. stash_a.go with content: package main; func StashA() {}
   2. stash_b.go with content: package main; func StashB() {}
   3. stash_c.go with content: package main; func StashC() {}
   Create all three files, nothing else.
   ```

3. **Exit Droid**, commit A only:
   ```bash
   git add stash_a.go
   git commit -m "Add stash_a"
   ```

4. **Stash remaining files:**
   ```bash
   git stash -u
   ls stash_b.go stash_c.go 2>/dev/null && echo "FAIL: files should be stashed" || echo "PASS: files stashed"
   ```

5. **Start Droid again (Prompt 2):**
   ```
   Create these files:
   1. stash_d.go with content: package main; func StashD() {}
   2. stash_e.go with content: package main; func StashE() {}
   Create both files, nothing else.
   ```

6. **Exit Droid**, commit D and E:
   ```bash
   git add stash_d.go stash_e.go
   git commit -m "Add stash_d and stash_e"
   ```

7. **Verify both commits have checkpoint IDs:**
   ```bash
   git log --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}'
   # Should show at least 2 unique IDs
   ```

8. **Validate checkpoint metadata:**
   ```bash
   # Most recent checkpoint (D, E)
   CPID=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   SHARD="${CPID:0:2}/${CPID:2}"
   git show "entire/checkpoints/v1:${SHARD}/metadata.json" | jq '.files_touched'
   # Should include stash_d.go and stash_e.go
   ```

#### Expected Outcome
- First commit (A) has checkpoint
- Second commit (D, E) has checkpoint
- `files_touched` is correct for each checkpoint
- B and C remain stashed

---

### Test 18: Scenario 6 – Stash + Second Prompt + Unstash + Commit All

**What it validates:** Stash, run another prompt, unstash, commit all files together.

**Corresponds to:** `TestE2E_Scenario6_StashSecondPromptUnstashCommitAll`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid (Prompt 1):**
   ```
   Create these files:
   1. combo_a.go with content: package main; func ComboA() {}
   2. combo_b.go with content: package main; func ComboB() {}
   3. combo_c.go with content: package main; func ComboC() {}
   Create all three files, nothing else.
   ```

3. **Exit Droid**, commit A only:
   ```bash
   git add combo_a.go
   git commit -m "Add combo_a"
   ```

4. **Stash B and C:**
   ```bash
   git stash -u
   ```

5. **Start Droid again (Prompt 2):**
   ```
   Create these files:
   1. combo_d.go with content: package main; func ComboD() {}
   2. combo_e.go with content: package main; func ComboE() {}
   Create both files, nothing else.
   ```

6. **Exit Droid**, then unstash:
   ```bash
   git stash pop
   ls combo_b.go combo_c.go  # Should be back
   ```

7. **Commit ALL remaining files together:**
   ```bash
   git add combo_b.go combo_c.go combo_d.go combo_e.go
   git commit -m "Add combo_b, combo_c, combo_d, combo_e"
   ```

8. **Verify:**
   ```bash
   CPIDS=$(git log --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   echo "Checkpoint IDs: $CPIDS"
   # Should be at least 2 unique IDs

   CPID=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   SHARD="${CPID:0:2}/${CPID:2}"
   git show "entire/checkpoints/v1:${SHARD}/metadata.json" | jq '.files_touched'
   # Should include all 4 files: combo_b.go, combo_c.go, combo_d.go, combo_e.go
   ```

9. **Verify no shadow branches remain:**
   ```bash
   git branch -a | grep "entire/" | grep -v "checkpoints"
   ```

#### Expected Outcome
- Combined commit has all 4 files in `files_touched`
- Two unique checkpoint IDs across the two commits
- No shadow branches remain

---

### Test 19: Scenario 7 – Partial Staging (Simulated)

**What it validates:** Content-aware carry-forward detects partial commits via hash comparison.

**Corresponds to:** `TestE2E_Scenario7_PartialStagingSimulated`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Create a placeholder file and commit it (so it's a tracked/modified file):**
   ```bash
   echo 'package main

   // placeholder' > partial.go
   git add partial.go
   git commit -m "Add placeholder partial.go"
   ```

3. **Start Droid** and type:
   ```
   Replace the contents of partial.go with this exact content:
   package main

   func First() int { return 1 }
   func Second() int { return 2 }
   func Third() int { return 3 }
   func Fourth() int { return 4 }

   Replace the file with exactly this content, nothing else.
   ```

4. **Exit Droid**, then save the full content:
   ```bash
   cp partial.go partial_full.go  # Backup
   ```

5. **Write partial content (first two functions only) and commit:**
   ```bash
   cat > partial.go << 'EOF'
   package main

   func First() int {
   	return 1
   }

   func Second() int {
   	return 2
   }
   EOF

   git add partial.go
   git commit -m "Add first two functions"
   ```

6. **Restore the full content and commit the rest:**
   ```bash
   cp partial_full.go partial.go
   git add partial.go
   git commit -m "Add remaining functions"
   ```

7. **Verify both commits have unique checkpoint IDs:**
   ```bash
   git log --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}' | sort -u
   # Should show 2 unique IDs
   ```

#### Expected Outcome
- Both commits get checkpoint trailers
- Checkpoint IDs are unique
- Content-aware carry-forward detects partial commit (hash mismatch)

---

## Content-Aware Detection Tests

### Test 20: ContentAwareOverlap_RevertAndReplace

**What it validates:** When user reverts agent's new file and writes completely different content, NO checkpoint trailer is added.

**Corresponds to:** `TestE2E_ContentAwareOverlap_RevertAndReplace`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid** and type:
   ```
   Create a file called overlap_test.go with this exact content:
   package main

   func OverlapOriginal() string {
   	return "original content from agent"
   }

   Create only this file.
   ```

3. **Exit Droid**, verify rewind points:
   ```bash
   entire rewind --list | jq 'length'  # At least 1
   ```

4. **Revert and write completely different content:**
   ```bash
   cat > overlap_test.go << 'EOF'
   package main

   func CompletelyDifferent() string {
   	return "user wrote this, not the agent"
   }
   EOF
   ```

5. **Commit:**
   ```bash
   git add overlap_test.go
   git commit -m "Add overlap test file"
   ```

6. **Verify NO checkpoint trailer was added:**
   ```bash
   git log -1 --format=%B | grep "Entire-Checkpoint:" && echo "FAIL: Trailer should not exist" || echo "PASS: No trailer"
   ```

#### Expected Outcome
- Commit is made but has NO `Entire-Checkpoint` trailer
- Content-aware detection prevents linking because the user replaced the agent's content entirely (new file + content hash mismatch)

---

## Existing Files Tests

### Test 21: ExistingFiles_ModifyAndCommit

**What it validates:** Agent modifying an existing tracked file gets proper checkpoint.

**Corresponds to:** `TestE2E_ExistingFiles_ModifyAndCommit`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Create and commit an initial file:**
   ```bash
   cat > config.go << 'EOF'
   package main

   var Config = map[string]string{
   	"version": "1.0",
   }
   EOF
   git add config.go
   git commit -m "Add initial config"
   ```

3. **Start Droid** and type:
   ```
   Modify the file config.go to add a new config key "debug" with value "true".
   Keep the existing content and just add the new key. Only modify this one file.
   ```

4. **Exit Droid**, verify modification:
   ```bash
   grep "debug" config.go && echo "PASS: debug key added"
   ```

5. **Commit:**
   ```bash
   git add config.go
   git commit -m "Add debug config"
   ```

6. **Verify checkpoint:**
   ```bash
   git log -1 --format=%B | grep "Entire-Checkpoint:"
   # Should have trailer
   ```

#### Expected Outcome
- `config.go` contains the new "debug" key
- Commit has checkpoint trailer

---

### Test 22: ExistingFiles_StashModifications

**What it validates:** Stashing modifications to tracked files works correctly with checkpoints.

**Corresponds to:** `TestE2E_ExistingFiles_StashModifications`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Create and commit two files:**
   ```bash
   echo 'package main

   func A() { /* original */ }' > fileA.go
   echo 'package main

   func B() { /* original */ }' > fileB.go
   git add fileA.go fileB.go
   git commit -m "Add initial files"
   ```

3. **Start Droid** and type:
   ```
   Modify these files:
   1. In fileA.go, change the comment from "original" to "modified by agent"
   2. In fileB.go, change the comment from "original" to "modified by agent"
   Only modify these two files.
   ```

4. **Exit Droid**, commit only fileA.go:
   ```bash
   git add fileA.go
   git commit -m "Update fileA"
   CP1=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   echo "Checkpoint 1: $CP1"
   ```

5. **Stash fileB.go:**
   ```bash
   git stash
   grep "original" fileB.go && echo "PASS: fileB.go reverted"
   ```

6. **Pop stash and commit fileB.go:**
   ```bash
   git stash pop
   grep "modified by agent" fileB.go && echo "PASS: fileB.go has agent changes"
   git add fileB.go
   git commit -m "Update fileB"
   CP2=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   echo "Checkpoint 2: $CP2"
   ```

7. **Verify unique checkpoints:**
   ```bash
   [ "$CP1" != "$CP2" ] && echo "PASS: Unique checkpoints" || echo "FAIL"
   ```

#### Expected Outcome
- Both commits have unique checkpoint IDs
- Stash/pop of tracked file modifications works correctly
- Both files end up with agent modifications committed

---

### Test 23: ExistingFiles_SplitCommits

**What it validates:** User splitting agent's modifications to multiple existing files into separate commits.

**Corresponds to:** `TestE2E_ExistingFiles_SplitCommits`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Create and commit MVC scaffolding:**
   ```bash
   echo 'package main

   type Model struct{}' > model.go
   echo 'package main

   type View struct{}' > view.go
   echo 'package main

   type Controller struct{}' > controller.go
   git add model.go view.go controller.go
   git commit -m "Add MVC scaffolding"
   ```

3. **Start Droid** and type:
   ```
   Add a Name field (string type) to each struct in these files:
   1. model.go - add Name string to Model struct
   2. view.go - add Name string to View struct
   3. controller.go - add Name string to Controller struct
   Only modify these three files.
   ```

4. **Exit Droid**, then commit each file separately:
   ```bash
   git add model.go && git commit -m "Add Name to Model"
   CP_MODEL=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')

   git add view.go && git commit -m "Add Name to View"
   CP_VIEW=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')

   git add controller.go && git commit -m "Add Name to Controller"
   CP_CTRL=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')

   echo "Model: $CP_MODEL, View: $CP_VIEW, Controller: $CP_CTRL"
   ```

5. **Verify all three are unique:**
   ```bash
   [ "$CP_MODEL" != "$CP_VIEW" ] && [ "$CP_VIEW" != "$CP_CTRL" ] && [ "$CP_MODEL" != "$CP_CTRL" ] \
     && echo "PASS: All unique" || echo "FAIL"
   ```

6. **Verify metadata for each:**
   ```bash
   for CPID in $CP_MODEL $CP_VIEW $CP_CTRL; do
     SHARD="${CPID:0:2}/${CPID:2}"
     echo "--- Checkpoint $CPID ---"
     git show "entire/checkpoints/v1:${SHARD}/metadata.json" | jq '.files_touched'
   done
   ```

7. **Verify no shadow branches remain:**
   ```bash
   git branch -a | grep "entire/" | grep -v "checkpoints"
   ```

#### Expected Outcome
- Three commits, each with unique checkpoint IDs
- Each checkpoint has correct `files_touched` (single file each)
- No shadow branches remain

---

### Test 24: ExistingFiles_RevertModification

**What it validates:** Modified files (existing in HEAD) ALWAYS get checkpoints, even when user replaces content.

**Corresponds to:** `TestE2E_ExistingFiles_RevertModification`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Create and commit a placeholder:**
   ```bash
   echo 'package main

   // placeholder' > calc.go
   git add calc.go
   git commit -m "Add placeholder"
   ```

3. **Start Droid** and type:
   ```
   Replace the contents of calc.go with this exact code:
   package main

   func AgentMultiply(a, b int) int {
   	return a * b
   }

   Only modify calc.go, nothing else.
   ```

4. **Exit Droid**, verify agent modified it:
   ```bash
   grep "AgentMultiply" calc.go && echo "PASS"
   ```

5. **Revert and write completely different content:**
   ```bash
   cat > calc.go << 'EOF'
   package main

   func UserAdd(x, y int) int {
   	return x + y
   }
   EOF
   ```

6. **Commit:**
   ```bash
   git add calc.go
   git commit -m "Add user functions"
   ```

7. **Verify checkpoint IS present (modified files always get checkpoints):**
   ```bash
   git log -1 --format=%B | grep "Entire-Checkpoint:" && echo "PASS: Checkpoint present" || echo "FAIL"
   ```

#### Expected Outcome
- Checkpoint trailer IS added even though user replaced the content
- This is intentional: for modified files (existing in HEAD), content-aware detection does not apply — the file was touched by the session

---

### Test 25: ExistingFiles_MixedNewAndModified

**What it validates:** Agent creating new files AND modifying existing files in the same session.

**Corresponds to:** `TestE2E_ExistingFiles_MixedNewAndModified`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Create and commit an existing file:**
   ```bash
   cat > main.go << 'EOF'
   package main

   func main() {
   	// TODO: add imports
   }
   EOF
   git add main.go
   git commit -m "Add main.go"
   ```

3. **Start Droid** and type:
   ```
   Do these tasks:
   1. Create a new file utils.go with: package main; func Helper() string { return "helper" }
   2. Create a new file types.go with: package main; type Config struct { Name string }
   3. Modify main.go to add a comment "// imports utils and types" at the top (after package main)
   Complete all three tasks.
   ```

4. **Exit Droid**, commit the modified file first:
   ```bash
   git add main.go
   git commit -m "Update main.go imports comment"
   CP1=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   ```

5. **Commit the new files:**
   ```bash
   git add utils.go types.go
   git commit -m "Add utils and types"
   CP2=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   ```

6. **Verify:**
   ```bash
   [ -n "$CP1" ] && [ -n "$CP2" ] && [ "$CP1" != "$CP2" ] \
     && echo "PASS: Both have unique checkpoints" || echo "FAIL"
   ```

#### Expected Outcome
- Modified file commit has checkpoint
- New files commit has checkpoint
- Different checkpoint IDs

---

## Session Lifecycle Tests

### Test 26: EndedSession_UserCommitsAfterExit

**What it validates:** After agent exits (session ends), user commits still get checkpoint trailers.

**Corresponds to:** `TestE2E_EndedSession_UserCommitsAfterExit`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid** and type:
   ```
   Create these files:
   1. ended_a.go with content: package main; func EndedA() {}
   2. ended_b.go with content: package main; func EndedB() {}
   3. ended_c.go with content: package main; func EndedC() {}
   Create all three files, nothing else.
   ```

3. **Exit Droid** (session is now in ENDED state).

4. **Commit A and B together:**
   ```bash
   git add ended_a.go ended_b.go
   git commit -m "Add ended files A and B"
   CPID_AB=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   echo "Checkpoint A,B: $CPID_AB"
   ```

5. **Commit C:**
   ```bash
   git add ended_c.go
   git commit -m "Add ended file C"
   CPID_C=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   echo "Checkpoint C: $CPID_C"
   ```

6. **Verify unique checkpoints:**
   ```bash
   [ "$CPID_AB" != "$CPID_C" ] && echo "PASS" || echo "FAIL"
   ```

7. **Validate metadata:**
   ```bash
   SHARD="${CPID_AB:0:2}/${CPID_AB:2}"
   git show "entire/checkpoints/v1:${SHARD}/metadata.json" | jq '.files_touched'
   # Should include ended_a.go, ended_b.go

   SHARD="${CPID_C:0:2}/${CPID_C:2}"
   git show "entire/checkpoints/v1:${SHARD}/metadata.json" | jq '.files_touched'
   # Should include ended_c.go
   ```

8. **Verify no shadow branches remain:**
   ```bash
   git branch -a | grep "entire/" | grep -v "checkpoints"
   ```

#### Expected Outcome
- Both post-exit commits get checkpoint trailers
- Unique checkpoint IDs
- Correct `files_touched` for each
- Session ENDED + GitCommit path works correctly

---

### Test 27: DeletedFiles_CommitDeletion

**What it validates:** Deleting a file tracked by the session and committing the deletion.

**Corresponds to:** `TestE2E_DeletedFiles_CommitDeletion`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Create a file to be deleted:**
   ```bash
   echo 'package main

   func ToDelete() {}' > to_delete.go
   git add to_delete.go
   git commit -m "Add to_delete.go"
   ```

3. **Start Droid** and type:
   ```
   Do these two tasks:
   1. Delete the file to_delete.go (use: rm to_delete.go)
   2. Create a new file replacement.go with content: package main; func Replacement() {}
   Do both tasks.
   ```

4. **Exit Droid**, verify state:
   ```bash
   ls to_delete.go 2>/dev/null && echo "FAIL: should be deleted" || echo "PASS: deleted"
   cat replacement.go
   ```

5. **Commit the replacement first:**
   ```bash
   git add replacement.go
   git commit -m "Add replacement"
   ```

6. **Commit the deletion:**
   ```bash
   git rm to_delete.go 2>/dev/null || true  # May already be deleted from working tree
   git commit -m "Remove to_delete.go"
   ```

7. **Check checkpoint trailers:**
   ```bash
   git log --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}'
   ```

#### Expected Outcome
- Replacement file commit has checkpoint trailer
- Deletion commit may or may not have trailer (deleted files may not carry forward)
- Both operations complete without errors

---

### Test 28: AgentCommitsMidTurn_UserCommitsRemainder

**What it validates:** Agent commits some files mid-turn, user commits the rest after.

**Corresponds to:** `TestE2E_AgentCommitsMidTurn_UserCommitsRemainder`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid** and type:
   ```
   Do these tasks in order:
   1. Create file agent_mid1.go with content: package main; func AgentMid1() {}
   2. Create file agent_mid2.go with content: package main; func AgentMid2() {}
   3. Commit these two files: git add agent_mid1.go agent_mid2.go && git commit -m "Agent adds mid1 and mid2"
   4. Create file user_remainder.go with content: package main; func UserRemainder() {}

   Do all tasks in order. Create each file, then commit the first two, then create the third.
   ```

3. **Exit Droid**, verify all files:
   ```bash
   ls agent_mid1.go agent_mid2.go user_remainder.go
   ```

4. **Commit the remaining file:**
   ```bash
   git add user_remainder.go
   git commit -m "Add user remainder"
   ```

5. **Check all checkpoint IDs are unique:**
   ```bash
   git log --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}' | sort -u
   ```

6. **Validate user's checkpoint:**
   ```bash
   CPID=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   SHARD="${CPID:0:2}/${CPID:2}"
   git show "entire/checkpoints/v1:${SHARD}/metadata.json" | jq '.files_touched'
   # Should include user_remainder.go
   ```

7. **Verify no shadow branches remain:**
   ```bash
   git branch -a | grep "entire/" | grep -v "checkpoints"
   ```

#### Expected Outcome
- Agent's mid-turn commit has checkpoint
- User's remainder commit has a different checkpoint
- `user_remainder.go` correctly in `files_touched`
- No shadow branches remain

---

### Test 29: TrailerRemoval_SkipsCondensation

**What it validates:** Removing the `Entire-Checkpoint` trailer from a commit message prevents condensation.

**Corresponds to:** `TestE2E_TrailerRemoval_SkipsCondensation`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid**, create a file, exit Droid:
   ```
   Create a file called trailer_test.go with content:
   package main
   func TrailerTest() {}
   Create only this file.
   ```

3. **Count existing checkpoint IDs:**
   ```bash
   BEFORE=$(git log --format=%B | grep -c "Entire-Checkpoint:" || echo 0)
   ```

4. **Commit with trailer removal (use `git commit` with editor to remove the trailer):**
   ```bash
   git add trailer_test.go
   # Option A: Use GIT_EDITOR to remove the trailer automatically
   GIT_EDITOR="sed -i '' '/Entire-Checkpoint:/d'" git commit -m "Add trailer_test (no checkpoint)"
   # Option B: Or manually edit the commit message in your editor to remove the trailer line
   ```

5. **Verify trailer was removed:**
   ```bash
   git log -1 --format=%B | grep "Entire-Checkpoint:" && echo "FAIL" || echo "PASS: No trailer"
   ```

6. **Verify no new checkpoint was created:**
   ```bash
   AFTER=$(git log --format=%B | grep -c "Entire-Checkpoint:" || echo 0)
   [ "$BEFORE" -eq "$AFTER" ] && echo "PASS: No new checkpoint" || echo "FAIL"
   ```

#### Expected Outcome
- Commit message does NOT have `Entire-Checkpoint` trailer
- No new checkpoint created
- User can opt out of checkpointing by removing the trailer

---

### Test 30: SessionDepleted_ManualEditNoCheckpoint

**What it validates:** After all session files are committed, subsequent manual edits do NOT get checkpoint trailers.

**Corresponds to:** `TestE2E_SessionDepleted_ManualEditNoCheckpoint`

#### Steps

1. [Common Setup](#common-setup) with `manual-commit` strategy.

2. **Start Droid**, create a file, exit Droid:
   ```
   Create a file called depleted.go with content:
   package main
   func Depleted() {}
   Create only this file.
   ```

3. **Commit the agent's file (gets checkpoint):**
   ```bash
   git add depleted.go
   git commit -m "Add depleted.go"
   CP_COUNT=$(git log --format=%B | grep -c "Entire-Checkpoint:" || echo 0)
   echo "Checkpoints so far: $CP_COUNT"
   ```

4. **Manually edit the file (no Droid involved):**
   ```bash
   cat > depleted.go << 'EOF'
   package main

   // Manual user edit
   func Depleted() { return }
   EOF
   ```

5. **Commit the manual edit:**
   ```bash
   git add depleted.go
   git commit -m "Manual edit to depleted.go"
   ```

6. **Verify NO new checkpoint was created:**
   ```bash
   NEW_COUNT=$(git log --format=%B | grep -c "Entire-Checkpoint:" || echo 0)
   [ "$NEW_COUNT" -eq "$CP_COUNT" ] && echo "PASS: No new checkpoint for manual edit" || echo "FAIL"
   ```

#### Expected Outcome
- Agent's file gets checkpoint when committed
- Manual edit after session depletion does NOT get checkpoint
- Session correctly tracks that all agent files have been committed

---

## Resume Tests

### Test 31: ResumeInRelocatedRepo

**What it validates:** `entire resume` works when a repository is moved to a different location.

**Corresponds to:** `TestE2E_ResumeInRelocatedRepo`

#### Steps

1. **Create a test repo at original location:**
   ```bash
   ORIG_DIR=$(mktemp -d)/original-repo
   mkdir -p "$ORIG_DIR"
   cd "$ORIG_DIR"
   git init
   git commit --allow-empty -m "Initial commit"
   git checkout -b feature/resume-test
   entire enable --agent factoryai-droid --strategy manual-commit --telemetry=false --force
   git add . && git commit -m "Add entire config"
   ```

2. **Start Droid**, create a file, exit Droid:
   ```
   Create a file called hello.go with a simple Go program that prints "Hello, World!".
   ```

3. **Commit to create a checkpoint:**
   ```bash
   git add hello.go
   git commit -m "Add hello world"
   CPID=$(git log -1 --format=%B | grep "Entire-Checkpoint:" | awk '{print $2}')
   echo "Checkpoint: $CPID"
   ```

4. **Move the repo to a new location:**
   ```bash
   NEW_DIR=$(mktemp -d)/relocated/new-location/test-repo
   mkdir -p "$(dirname "$NEW_DIR")"
   mv "$ORIG_DIR" "$NEW_DIR"
   cd "$NEW_DIR"
   ```

5. **Run `entire resume`:**
   ```bash
   entire resume feature/resume-test --force
   ```

6. **Verify the output references the NEW location**, not the old one:
   ```bash
   # The resume output should show the new session directory path
   # Transcript files should be at the new location
   ```

7. **Verify the old location was NOT created:**
   ```bash
   ls "$ORIG_DIR" 2>/dev/null && echo "FAIL: Old dir exists" || echo "PASS: Old dir gone"
   ```

#### Expected Outcome
- `entire resume` succeeds at the new location
- Transcript is written to the new location's session directory
- Old location is not referenced or created
- Location-independent path resolution works correctly

---

## Quick Reference: Test Setup Script

Use this script to quickly create test repos:

```bash
#!/bin/bash
# Usage: ./setup-test-repo.sh [strategy]
# Default strategy: manual-commit

STRATEGY=${1:-manual-commit}
TEST_DIR=$(mktemp -d)
echo "Test repo: $TEST_DIR"

cd "$TEST_DIR"
git init
git config user.name "Test User"
git config user.email "test@example.com"
git commit --allow-empty -m "Initial commit"
git checkout -b feature/manual-test

entire enable --agent factoryai-droid --strategy "$STRATEGY" --telemetry=false --force
git add .
git commit -m "Add entire and agent config"

echo ""
echo "Ready! cd $TEST_DIR && droid"
```

## Cleanup

After testing, remove test directories:

```bash
rm -rf /tmp/tmp.*  # Remove all temp dirs (be careful with this!)
```
