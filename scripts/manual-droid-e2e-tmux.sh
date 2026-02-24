#!/usr/bin/env bash

set -u
set -o pipefail

# Automates a deterministic subset of cmd/entire/cli/manual-droid-e2e-testing.md
# by driving interactive Droid sessions through tmux panes.
#
# Default suite ("smoke"):
# - Test 1: BasicWorkflow
# - Test 2: MultipleChanges
# - Test 3: CheckpointMetadata
# - Test 4: CheckpointIDFormat
# - Test 5: AutoCommitStrategy
# - Test 8: RewindToCheckpoint
# - Test 9: RewindAfterCommit
# - Test 10: RewindMultipleFiles
# - Test 20: ContentAwareOverlap_RevertAndReplace
# - Test 30: SessionDepleted_ManualEditNoCheckpoint
#
# Usage:
#   ./scripts/manual-droid-e2e-tmux.sh
#   ./scripts/manual-droid-e2e-tmux.sh --tests test_01_basic_workflow,test_05_auto_commit_strategy
#   ./scripts/manual-droid-e2e-tmux.sh --keep-repos

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SELF_DIR}/.." && pwd)"

ENTIRE_BIN="${ENTIRE_BIN:-entire}"
DROID_BIN="${DROID_BIN:-droid}"
USE_SYSTEM_ENTIRE="${USE_SYSTEM_ENTIRE:-0}"

PROMPT_TIMEOUT_SECONDS="${PROMPT_TIMEOUT_SECONDS:-240}"
STARTUP_TIMEOUT_SECONDS="${STARTUP_TIMEOUT_SECONDS:-60}"
QUIET_SECONDS="${QUIET_SECONDS:-8}"
POST_EXIT_TIMEOUT_SECONDS="${POST_EXIT_TIMEOUT_SECONDS:-20}"
TEST_PAUSE_SECONDS="${TEST_PAUSE_SECONDS:-3}"

KEEP_REPOS=0
TEST_FILTER=""

RESULT_PASS=0
RESULT_FAIL=0
declare -a RESULT_LINES

RUN_ROOT=""
CURRENT_TEST_LOG=""
CURRENT_TEST_REPO=""
LAST_ERROR=""
NEW_TEST_REPO=""

PROMPT_CREATE_HELLO='Create a file called hello.go with a simple Go program that prints "Hello, World!".
Requirements:
- Use package main
- Use a main function
- Use fmt.Println to print exactly "Hello, World!"
- Do not add comments, tests, or extra functionality
- Do not create any other files'

PROMPT_CREATE_CALC='Create a file called calc.go with two exported functions:
- Add(a, b int) int - returns a + b
- Subtract(a, b int) int - returns a - b
Requirements:
- Use package main
- No comments or documentation
- No main function
- No tests
- No other files'

PROMPT_CREATE_CONFIG='Create a file called config.json with this exact content:
{
  "name": "e2e-test",
  "version": "1.0.0",
  "enabled": true
}
Do not create any other files.'

PROMPT_MODIFY_HELLO='Modify hello.go to print "Hello, E2E Test!" instead of "Hello, World!".
Do not add any other functionality or files.'

PROMPT_CREATE_OVERLAP='Create a file called overlap_test.go with this exact content:
package main

func OverlapOriginal() string {
	return "original content from agent"
}

Create only this file.'

PROMPT_CREATE_DEPLETED='Create a file called depleted.go with content:
package main
func Depleted() {}
Create only this file.'

usage() {
	cat <<EOF
Usage: $0 [options]

Options:
  --tests <csv>      Run only selected test function names
  --keep-repos       Keep temporary test repos
  --help             Show this help

Environment:
  ENTIRE_BIN                   Entire CLI binary (default: entire)
  DROID_BIN                    Droid CLI binary (default: droid)
  USE_SYSTEM_ENTIRE            Set to 1 to skip building local entire binary
  PROMPT_TIMEOUT_SECONDS       Timeout waiting for a prompt to settle (default: 240)
  STARTUP_TIMEOUT_SECONDS      Timeout waiting for droid startup (default: 60)
  QUIET_SECONDS                Required quiet window in tmux output (default: 8)
  POST_EXIT_TIMEOUT_SECONDS    Timeout waiting after /exit (default: 20)
  TEST_PAUSE_SECONDS           Delay between tests to reduce API pressure (default: 3)
EOF
}

parse_args() {
	while [[ $# -gt 0 ]]; do
		case "$1" in
			--tests)
				TEST_FILTER="${2:-}"
				shift 2
				;;
			--keep-repos)
				KEEP_REPOS=1
				shift
				;;
			--help|-h)
				usage
				exit 0
				;;
			*)
				echo "Unknown argument: $1" >&2
				usage >&2
				exit 2
				;;
		esac
	done
}

append_result() {
	local status="$1"
	local test_name="$2"
	local detail="$3"
	RESULT_LINES+=("${status}|${test_name}|${detail}")
	if [[ "${status}" == "PASS" ]]; then
		RESULT_PASS=$((RESULT_PASS + 1))
	else
		RESULT_FAIL=$((RESULT_FAIL + 1))
	fi
}

require_binary() {
	local name="$1"
	if ! command -v "${name}" >/dev/null 2>&1; then
		LAST_ERROR="required command not found: ${name}"
		return 1
	fi
	return 0
}

preflight() {
	local failures=0

	require_binary "git" || {
		echo "Preflight: ${LAST_ERROR}" >&2
		failures=$((failures + 1))
	}
	if [[ "${USE_SYSTEM_ENTIRE}" != "1" ]]; then
		require_binary "go" || {
			echo "Preflight: ${LAST_ERROR}" >&2
			failures=$((failures + 1))
		}
	fi
	require_binary "${ENTIRE_BIN}" || {
		echo "Preflight: ${LAST_ERROR}" >&2
		failures=$((failures + 1))
	}
	require_binary "${DROID_BIN}" || {
		echo "Preflight: ${LAST_ERROR}" >&2
		failures=$((failures + 1))
	}
	require_binary "jq" || {
		echo "Preflight: ${LAST_ERROR}" >&2
		failures=$((failures + 1))
	}
	require_binary "tmux" || {
		echo "Preflight: ${LAST_ERROR}" >&2
		failures=$((failures + 1))
	}

	if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
		echo "Preflight: ANTHROPIC_API_KEY is not set" >&2
		failures=$((failures + 1))
	fi

	if [[ ${failures} -gt 0 ]]; then
		return 1
	fi
	return 0
}

prepare_entire_binary() {
	local build_dir
	build_dir="$(mktemp -d "${RUN_ROOT}/entire-bin.XXXXXX")" || {
		LAST_ERROR="failed creating temp directory for entire binary"
		return 1
	}

	if ! go build -o "${build_dir}/entire" "${REPO_ROOT}/cmd/entire" >/dev/null 2>&1; then
		LAST_ERROR="failed to build entire binary from ${REPO_ROOT}/cmd/entire"
		return 1
	fi

	ENTIRE_BIN="${build_dir}/entire"
	export PATH="${build_dir}:${PATH}"
	return 0
}

run_in_repo() {
	local repo="$1"
	shift
	(
		cd "${repo}" && "$@"
	)
}

new_test_repo() {
	local strategy="$1"
	local test_name="$2"
	local repo_dir
	local safe_test_name
	NEW_TEST_REPO=""
	safe_test_name="$(echo "${test_name}" | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9_' '_')"
	repo_dir="$(mktemp -d "${RUN_ROOT}/${safe_test_name}.XXXXXX")" || return 1

	if ! run_in_repo "${repo_dir}" git init >/dev/null 2>&1; then
		LAST_ERROR="git init failed in ${repo_dir}"
		return 1
	fi
	run_in_repo "${repo_dir}" git config user.name "Test User" >/dev/null 2>&1 || true
	run_in_repo "${repo_dir}" git config user.email "test@example.com" >/dev/null 2>&1 || true
	run_in_repo "${repo_dir}" git config commit.gpgsign false >/dev/null 2>&1 || true

	if ! run_in_repo "${repo_dir}" git commit --allow-empty -m "Initial commit" >/dev/null 2>&1; then
		LAST_ERROR="initial commit failed in ${repo_dir}"
		return 1
	fi
	if ! run_in_repo "${repo_dir}" git checkout -b feature/manual-test >/dev/null 2>&1; then
		LAST_ERROR="failed to create feature/manual-test branch in ${repo_dir}"
		return 1
	fi

	if ! run_in_repo "${repo_dir}" "${ENTIRE_BIN}" enable --agent factoryai-droid --strategy "${strategy}" --telemetry=false --force >/dev/null 2>&1; then
		LAST_ERROR="entire enable failed (strategy=${strategy}) in ${repo_dir}"
		return 1
	fi
	if ! run_in_repo "${repo_dir}" git add . >/dev/null 2>&1; then
		LAST_ERROR="git add . failed after entire enable in ${repo_dir}"
		return 1
	fi
	if ! run_in_repo "${repo_dir}" git commit -m "Add entire and agent config" >/dev/null 2>&1; then
		LAST_ERROR="failed committing entire config in ${repo_dir}"
		return 1
	fi

	NEW_TEST_REPO="${repo_dir}"
	return 0
}

tmux_send_text() {
	local session="$1"
	local text="$2"
	local buffer_name="entire-e2e-buffer-$$"
	tmux set-buffer -b "${buffer_name}" -- "${text}" >/dev/null 2>&1 || return 1
	tmux paste-buffer -d -b "${buffer_name}" -t "${session}:0" >/dev/null 2>&1 || return 1
	tmux send-keys -t "${session}:0" C-m >/dev/null 2>&1 || return 1
	return 0
}

tmux_capture() {
	local session="$1"
	local out_file="$2"
	tmux capture-pane -p -S -200000 -t "${session}:0" > "${out_file}" 2>/dev/null || true
}

wait_for_tmux_quiet() {
	local session="$1"
	local timeout_seconds="$2"
	local quiet_seconds="$3"

	local started_at now last_change
	local prev_fingerprint fingerprint
	started_at="$(date +%s)"
	last_change="${started_at}"
	prev_fingerprint=""

	while true; do
		now="$(date +%s)"

		if ! tmux has-session -t "${session}" >/dev/null 2>&1; then
			LAST_ERROR="tmux session '${session}' exited unexpectedly"
			return 1
		fi

		fingerprint="$(
			tmux capture-pane -p -S -500 -t "${session}:0" 2>/dev/null \
				| cksum \
				| awk '{print $1 ":" $2}'
		)"

		if [[ "${fingerprint}" != "${prev_fingerprint}" ]]; then
			prev_fingerprint="${fingerprint}"
			last_change="${now}"
		fi

		if (( now - last_change >= quiet_seconds )); then
			return 0
		fi
		if (( now - started_at >= timeout_seconds )); then
			LAST_ERROR="timed out waiting for droid output to settle (${timeout_seconds}s)"
			return 1
		fi
		sleep 2
	done
}

run_droid_prompts_tmux() {
	local repo="$1"
	local log_name="$2"
	shift 2
	local prompts=("$@")
	local session="entire-droid-e2e-$RANDOM-$RANDOM"
	local log_dir="${repo}/.entire/manual-e2e-logs"
	local log_file="${log_dir}/${log_name}.tmux.log"

	mkdir -p "${log_dir}" || {
		LAST_ERROR="failed to create log directory: ${log_dir}"
		return 1
	}

	CURRENT_TEST_LOG="${log_file}"

	if ! tmux new-session -d -s "${session}" -c "${repo}" "${DROID_BIN}" >/dev/null 2>&1; then
		LAST_ERROR="failed to start droid in tmux session ${session}"
		return 1
	fi

	if ! wait_for_tmux_quiet "${session}" "${STARTUP_TIMEOUT_SECONDS}" "${QUIET_SECONDS}"; then
		tmux_capture "${session}" "${log_file}"
		tmux kill-session -t "${session}" >/dev/null 2>&1 || true
		return 1
	fi

	local prompt
	for prompt in "${prompts[@]}"; do
		if ! tmux_send_text "${session}" "${prompt}"; then
			LAST_ERROR="failed to send prompt to tmux session ${session}"
			tmux_capture "${session}" "${log_file}"
			tmux kill-session -t "${session}" >/dev/null 2>&1 || true
			return 1
		fi

		if ! wait_for_tmux_quiet "${session}" "${PROMPT_TIMEOUT_SECONDS}" "${QUIET_SECONDS}"; then
			tmux_capture "${session}" "${log_file}"
			tmux kill-session -t "${session}" >/dev/null 2>&1 || true
			return 1
		fi
	done

	tmux_send_text "${session}" "/exit" >/dev/null 2>&1 || true
	wait_for_tmux_quiet "${session}" "${POST_EXIT_TIMEOUT_SECONDS}" "${QUIET_SECONDS}" >/dev/null 2>&1 || true
	tmux_capture "${session}" "${log_file}"
	tmux kill-session -t "${session}" >/dev/null 2>&1 || true
	return 0
}

extract_latest_checkpoint_id() {
	local repo="$1"
	run_in_repo "${repo}" bash -lc "git log -1 --format=%B | awk '/Entire-Checkpoint:/ {print \$2; exit}'"
}

assert_checkpoint_format() {
	local checkpoint_id="$1"
	[[ "${checkpoint_id}" =~ ^[0-9a-f]{12}$ ]]
}

test_01_basic_workflow() {
	local repo
	new_test_repo "manual-commit" "test_01_basic_workflow" || return 1
	repo="${NEW_TEST_REPO}"
	CURRENT_TEST_REPO="${repo}"

	run_droid_prompts_tmux "${repo}" "test_01_basic_workflow" "${PROMPT_CREATE_HELLO}" || return 1
	[[ -f "${repo}/hello.go" ]] || {
		LAST_ERROR="hello.go was not created"
		return 1
	}

	local rewind_count
	rewind_count="$(run_in_repo "${repo}" bash -lc "entire rewind --list | jq 'length'" 2>/dev/null)" || {
		LAST_ERROR="failed to list rewind points"
		return 1
	}
	[[ "${rewind_count}" =~ ^[0-9]+$ ]] && (( rewind_count >= 1 )) || {
		LAST_ERROR="expected at least 1 rewind point, got: ${rewind_count}"
		return 1
	}

	run_in_repo "${repo}" git add hello.go >/dev/null 2>&1 || {
		LAST_ERROR="git add hello.go failed"
		return 1
	}
	run_in_repo "${repo}" git commit -m "Add hello world program" >/dev/null 2>&1 || {
		LAST_ERROR="git commit failed for hello.go"
		return 1
	}

	local cpid
	cpid="$(extract_latest_checkpoint_id "${repo}")"
	assert_checkpoint_format "${cpid}" || {
		LAST_ERROR="invalid or missing checkpoint id after commit: '${cpid}'"
		return 1
	}

	run_in_repo "${repo}" bash -lc "git branch -a | grep -q 'entire/checkpoints/v1'" >/dev/null 2>&1 || {
		LAST_ERROR="entire/checkpoints/v1 branch not found"
		return 1
	}

	return 0
}

test_02_multiple_changes() {
	local repo
	new_test_repo "manual-commit" "test_02_multiple_changes" || return 1
	repo="${NEW_TEST_REPO}"
	CURRENT_TEST_REPO="${repo}"

	run_droid_prompts_tmux "${repo}" "test_02_multiple_changes" \
		"${PROMPT_CREATE_HELLO}" \
		"${PROMPT_CREATE_CALC}" || return 1

	[[ -f "${repo}/hello.go" && -f "${repo}/calc.go" ]] || {
		LAST_ERROR="expected hello.go and calc.go to exist"
		return 1
	}

	local rewind_count
	rewind_count="$(run_in_repo "${repo}" bash -lc "entire rewind --list | jq 'length'" 2>/dev/null)" || {
		LAST_ERROR="failed to list rewind points"
		return 1
	}
	[[ "${rewind_count}" =~ ^[0-9]+$ ]] && (( rewind_count >= 2 )) || {
		LAST_ERROR="expected at least 2 rewind points, got: ${rewind_count}"
		return 1
	}

	run_in_repo "${repo}" git add hello.go calc.go >/dev/null 2>&1 || {
		LAST_ERROR="git add hello.go calc.go failed"
		return 1
	}
	run_in_repo "${repo}" git commit -m "Add hello world and calculator" >/dev/null 2>&1 || {
		LAST_ERROR="git commit failed"
		return 1
	}

	local cpid
	cpid="$(extract_latest_checkpoint_id "${repo}")"
	assert_checkpoint_format "${cpid}" || {
		LAST_ERROR="invalid or missing checkpoint id: '${cpid}'"
		return 1
	}

	return 0
}

test_03_checkpoint_metadata() {
	local repo
	new_test_repo "manual-commit" "test_03_checkpoint_metadata" || return 1
	repo="${NEW_TEST_REPO}"
	CURRENT_TEST_REPO="${repo}"

	run_droid_prompts_tmux "${repo}" "test_03_checkpoint_metadata" "${PROMPT_CREATE_CONFIG}" || return 1
	[[ -f "${repo}/config.json" ]] || {
		LAST_ERROR="config.json was not created"
		return 1
	}

	run_in_repo "${repo}" git add config.json >/dev/null 2>&1 || {
		LAST_ERROR="git add config.json failed"
		return 1
	}
	run_in_repo "${repo}" git commit -m "Add config file" >/dev/null 2>&1 || {
		LAST_ERROR="git commit failed for config.json"
		return 1
	}

	local cpid shard
	cpid="$(extract_latest_checkpoint_id "${repo}")"
	assert_checkpoint_format "${cpid}" || {
		LAST_ERROR="invalid or missing checkpoint id: '${cpid}'"
		return 1
	}
	shard="${cpid:0:2}/${cpid:2}"

	run_in_repo "${repo}" bash -lc "git show 'entire/checkpoints/v1:${shard}/metadata.json' | jq -e '.checkpoint_id and .strategy and .files_touched'" >/dev/null 2>&1 || {
		LAST_ERROR="checkpoint metadata.json missing required fields for ${cpid}"
		return 1
	}
	run_in_repo "${repo}" bash -lc "git show 'entire/checkpoints/v1:${shard}/0/metadata.json' | jq -e '.created_at'" >/dev/null 2>&1 || {
		LAST_ERROR="session metadata missing created_at for ${cpid}"
		return 1
	}

	return 0
}

test_04_checkpoint_id_format() {
	local repo
	new_test_repo "manual-commit" "test_04_checkpoint_id_format" || return 1
	repo="${NEW_TEST_REPO}"
	CURRENT_TEST_REPO="${repo}"

	run_droid_prompts_tmux "${repo}" "test_04_checkpoint_id_format" "${PROMPT_CREATE_HELLO}" || return 1
	run_in_repo "${repo}" git add hello.go >/dev/null 2>&1 || {
		LAST_ERROR="git add hello.go failed"
		return 1
	}
	run_in_repo "${repo}" git commit -m "Add hello world" >/dev/null 2>&1 || {
		LAST_ERROR="git commit failed"
		return 1
	}

	local cpid
	cpid="$(extract_latest_checkpoint_id "${repo}")"
	assert_checkpoint_format "${cpid}" || {
		LAST_ERROR="checkpoint id format invalid: '${cpid}'"
		return 1
	}

	return 0
}

test_05_auto_commit_strategy() {
	local repo
	new_test_repo "auto-commit" "test_05_auto_commit_strategy" || return 1
	repo="${NEW_TEST_REPO}"
	CURRENT_TEST_REPO="${repo}"

	local before_count after_count
	before_count="$(run_in_repo "${repo}" bash -lc "git log --oneline | wc -l | tr -d ' '")"
	run_droid_prompts_tmux "${repo}" "test_05_auto_commit_strategy" "${PROMPT_CREATE_HELLO}" || return 1
	after_count="$(run_in_repo "${repo}" bash -lc "git log --oneline | wc -l | tr -d ' '")"

	[[ "${before_count}" =~ ^[0-9]+$ && "${after_count}" =~ ^[0-9]+$ ]] || {
		LAST_ERROR="failed to read commit counts (before=${before_count}, after=${after_count})"
		return 1
	}
	(( after_count > before_count )) || {
		LAST_ERROR="auto-commit did not increase commit count (before=${before_count}, after=${after_count})"
		return 1
	}

	local cpid
	cpid="$(run_in_repo "${repo}" bash -lc "git log --format=%B | awk '/Entire-Checkpoint:/ {print \$2; exit}'")"
	assert_checkpoint_format "${cpid}" || {
		LAST_ERROR="missing/invalid checkpoint trailer for auto-commit run: '${cpid}'"
		return 1
	}

	return 0
}

test_08_rewind_to_checkpoint() {
	local repo
	new_test_repo "manual-commit" "test_08_rewind_to_checkpoint" || return 1
	repo="${NEW_TEST_REPO}"
	CURRENT_TEST_REPO="${repo}"

	local first_id
	run_droid_prompts_tmux "${repo}" "test_08_rewind_to_checkpoint_first" "${PROMPT_CREATE_HELLO}" || return 1
	first_id="$(run_in_repo "${repo}" bash -lc "entire rewind --list | jq -r '.[0].id'")"
	[[ -n "${first_id}" && "${first_id}" != "null" ]] || {
		LAST_ERROR="failed to capture first rewind checkpoint id"
		return 1
	}

	run_droid_prompts_tmux "${repo}" "test_08_rewind_to_checkpoint_second" "${PROMPT_MODIFY_HELLO}" || return 1

	run_in_repo "${repo}" bash -lc "grep -q 'E2E Test' hello.go" >/dev/null 2>&1 || {
		LAST_ERROR="hello.go did not contain modified content before rewind"
		return 1
	}

	run_in_repo "${repo}" entire rewind --to "${first_id}" >/dev/null 2>&1 || {
		LAST_ERROR="entire rewind --to ${first_id} failed"
		return 1
	}

	run_in_repo "${repo}" bash -lc "grep -q 'Hello, World!' hello.go" >/dev/null 2>&1 || {
		LAST_ERROR="hello.go did not restore original content after rewind"
		return 1
	}

	return 0
}

test_09_rewind_after_commit() {
	local repo
	new_test_repo "manual-commit" "test_09_rewind_after_commit" || return 1
	repo="${NEW_TEST_REPO}"
	CURRENT_TEST_REPO="${repo}"

	run_droid_prompts_tmux "${repo}" "test_09_rewind_after_commit" "${PROMPT_CREATE_HELLO}" || return 1

	local pre_id pre_logs_only
	pre_id="$(run_in_repo "${repo}" bash -lc "entire rewind --list | jq -r '.[0].id'")"
	pre_logs_only="$(run_in_repo "${repo}" bash -lc "entire rewind --list | jq -r '.[0].is_logs_only'")"

	[[ -n "${pre_id}" && "${pre_id}" != "null" ]] || {
		LAST_ERROR="failed to capture pre-commit rewind id"
		return 1
	}
	[[ "${pre_logs_only}" == "false" ]] || {
		LAST_ERROR="expected pre-commit rewind point to be non-logs-only; got ${pre_logs_only}"
		return 1
	}

	run_in_repo "${repo}" git add hello.go >/dev/null 2>&1 || {
		LAST_ERROR="git add hello.go failed"
		return 1
	}
	run_in_repo "${repo}" git commit -m "Add hello world" >/dev/null 2>&1 || {
		LAST_ERROR="git commit failed"
		return 1
	}

	local post_id post_logs_only
	post_id="$(run_in_repo "${repo}" bash -lc "entire rewind --list | jq -r '.[0].id'")"
	post_logs_only="$(run_in_repo "${repo}" bash -lc "entire rewind --list | jq -r '.[0].is_logs_only'")"
	[[ "${post_logs_only}" == "true" ]] || {
		LAST_ERROR="expected post-commit rewind point to be logs-only; got ${post_logs_only}"
		return 1
	}
	[[ "${post_id}" != "${pre_id}" ]] || {
		LAST_ERROR="expected post-commit rewind id to differ from pre-commit id"
		return 1
	}

	if run_in_repo "${repo}" entire rewind --to "${pre_id}" >/dev/null 2>&1; then
		LAST_ERROR="rewind to old pre-commit id unexpectedly succeeded"
		return 1
	fi

	return 0
}

test_10_rewind_multiple_files() {
	local repo
	new_test_repo "manual-commit" "test_10_rewind_multiple_files" || return 1
	repo="${NEW_TEST_REPO}"
	CURRENT_TEST_REPO="${repo}"

	run_droid_prompts_tmux "${repo}" "test_10_rewind_multiple_files" "${PROMPT_CREATE_HELLO}" || return 1

	local after_first
	after_first="$(run_in_repo "${repo}" bash -lc "entire rewind --list | jq -r '.[0].id'")"
	[[ -n "${after_first}" && "${after_first}" != "null" ]] || {
		LAST_ERROR="failed to capture rewind id after first file"
		return 1
	}

	run_droid_prompts_tmux "${repo}" "test_10_rewind_multiple_files_second_prompt" "${PROMPT_CREATE_CALC}" || return 1
	[[ -f "${repo}/hello.go" && -f "${repo}/calc.go" ]] || {
		LAST_ERROR="expected hello.go and calc.go before rewind"
		return 1
	}

	run_in_repo "${repo}" entire rewind --to "${after_first}" >/dev/null 2>&1 || {
		LAST_ERROR="rewind to ${after_first} failed"
		return 1
	}

	[[ -f "${repo}/hello.go" ]] || {
		LAST_ERROR="hello.go missing after rewind"
		return 1
	}
	[[ ! -f "${repo}/calc.go" ]] || {
		LAST_ERROR="calc.go should have been removed by rewind"
		return 1
	}

	return 0
}

test_20_content_aware_overlap_revert_and_replace() {
	local repo
	new_test_repo "manual-commit" "test_20_content_aware_overlap_revert_and_replace" || return 1
	repo="${NEW_TEST_REPO}"
	CURRENT_TEST_REPO="${repo}"

	run_droid_prompts_tmux "${repo}" "test_20_content_aware_overlap_revert_and_replace" "${PROMPT_CREATE_OVERLAP}" || return 1
	[[ -f "${repo}/overlap_test.go" ]] || {
		LAST_ERROR="overlap_test.go was not created"
		return 1
	}

	cat > "${repo}/overlap_test.go" <<'EOF'
package main

func CompletelyDifferent() string {
	return "user wrote this, not the agent"
}
EOF

	run_in_repo "${repo}" git add overlap_test.go >/dev/null 2>&1 || {
		LAST_ERROR="git add overlap_test.go failed"
		return 1
	}
	run_in_repo "${repo}" git commit -m "Add overlap test file" >/dev/null 2>&1 || {
		LAST_ERROR="git commit failed for overlap_test.go"
		return 1
	}

	if run_in_repo "${repo}" bash -lc "git log -1 --format=%B | grep -q 'Entire-Checkpoint:'"; then
		LAST_ERROR="unexpected checkpoint trailer for content replacement case"
		return 1
	fi

	return 0
}

test_30_session_depleted_manual_edit_no_checkpoint() {
	local repo
	new_test_repo "manual-commit" "test_30_session_depleted_manual_edit_no_checkpoint" || return 1
	repo="${NEW_TEST_REPO}"
	CURRENT_TEST_REPO="${repo}"

	run_droid_prompts_tmux "${repo}" "test_30_session_depleted_manual_edit_no_checkpoint" "${PROMPT_CREATE_DEPLETED}" || return 1
	[[ -f "${repo}/depleted.go" ]] || {
		LAST_ERROR="depleted.go was not created"
		return 1
	}

	run_in_repo "${repo}" git add depleted.go >/dev/null 2>&1 || {
		LAST_ERROR="git add depleted.go failed"
		return 1
	}
	run_in_repo "${repo}" git commit -m "Add depleted.go" >/dev/null 2>&1 || {
		LAST_ERROR="git commit failed for depleted.go"
		return 1
	}

	local before_count after_count
	before_count="$(run_in_repo "${repo}" bash -lc "git log --format=%B | grep -c 'Entire-Checkpoint:' || true")"

	cat > "${repo}/depleted.go" <<'EOF'
package main

// Manual user edit
func Depleted() { return }
EOF

	run_in_repo "${repo}" git add depleted.go >/dev/null 2>&1 || {
		LAST_ERROR="git add depleted.go (manual edit) failed"
		return 1
	}
	run_in_repo "${repo}" git commit -m "Manual edit to depleted.go" >/dev/null 2>&1 || {
		LAST_ERROR="git commit failed for manual edit"
		return 1
	}

	after_count="$(run_in_repo "${repo}" bash -lc "git log --format=%B | grep -c 'Entire-Checkpoint:' || true")"
	[[ "${before_count}" == "${after_count}" ]] || {
		LAST_ERROR="manual edit created a new checkpoint (before=${before_count}, after=${after_count})"
		return 1
	}

	return 0
}

run_single_test() {
	local test_name="$1"
	LAST_ERROR=""
	CURRENT_TEST_REPO=""
	CURRENT_TEST_LOG=""

	if ! declare -F "${test_name}" >/dev/null 2>&1; then
		append_result "FAIL" "${test_name}" "unknown test function"
		return
	fi

	echo "Running ${test_name}..."
	if "${test_name}"; then
		append_result "PASS" "${test_name}" "ok"
	else
		local detail="${LAST_ERROR}"
		if [[ -n "${CURRENT_TEST_REPO}" ]]; then
			detail="${detail}; repo=${CURRENT_TEST_REPO}"
		fi
		if [[ -n "${CURRENT_TEST_LOG}" ]]; then
			detail="${detail}; tmux_log=${CURRENT_TEST_LOG}"
		fi
		append_result "FAIL" "${test_name}" "${detail}"
	fi
}

print_summary() {
	echo
	echo "Results:"
	local line
	for line in "${RESULT_LINES[@]}"; do
		IFS='|' read -r status test_name detail <<< "${line}"
		printf "  %-4s %s\n" "${status}" "${test_name}"
		if [[ "${status}" == "FAIL" ]]; then
			printf "       %s\n" "${detail}"
		fi
	done
	echo
	echo "Passed: ${RESULT_PASS}"
	echo "Failed: ${RESULT_FAIL}"
}

cleanup() {
	if [[ ${KEEP_REPOS} -eq 0 && -n "${RUN_ROOT}" && -d "${RUN_ROOT}" ]]; then
		rm -rf "${RUN_ROOT}"
	else
		echo "Keeping test repos at: ${RUN_ROOT}"
	fi
}

main() {
	parse_args "$@"

	RUN_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/entire-droid-tmux-e2e.XXXXXX")"
	trap cleanup EXIT

	local -a tests=(
		"test_01_basic_workflow"
		"test_02_multiple_changes"
		"test_03_checkpoint_metadata"
		"test_04_checkpoint_id_format"
		"test_05_auto_commit_strategy"
		"test_08_rewind_to_checkpoint"
		"test_09_rewind_after_commit"
		"test_10_rewind_multiple_files"
		"test_20_content_aware_overlap_revert_and_replace"
		"test_30_session_depleted_manual_edit_no_checkpoint"
	)

	if [[ "${USE_SYSTEM_ENTIRE}" != "1" ]]; then
		if ! prepare_entire_binary; then
			echo "Failed preparing local entire binary: ${LAST_ERROR}"
			exit 2
		fi
	fi

	if ! preflight; then
		echo
		echo "Preflight failed. Install missing dependencies and retry."
		echo "Expected binaries: git, ${ENTIRE_BIN}, ${DROID_BIN}, jq, tmux"
		exit 2
	fi

	if [[ -n "${TEST_FILTER}" ]]; then
		IFS=',' read -r -a tests <<< "${TEST_FILTER}"
	fi

	local test_name
	for test_name in "${tests[@]}"; do
		run_single_test "${test_name}"
		if [[ "${TEST_PAUSE_SECONDS}" =~ ^[0-9]+$ ]] && (( TEST_PAUSE_SECONDS > 0 )); then
			sleep "${TEST_PAUSE_SECONDS}"
		fi
	done

	print_summary
	if [[ ${RESULT_FAIL} -gt 0 ]]; then
		exit 1
	fi
}

main "$@"
