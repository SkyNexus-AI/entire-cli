#!/bin/bash
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

SOURCE_BRANCH="entire/sessions"
TARGET_BRANCH="entire/sessions/v1"

echo -e "${GREEN}=== Checkpoint Migration Script ===${NC}"
echo "Source: $SOURCE_BRANCH"
echo "Target: $TARGET_BRANCH"
echo ""

# Save current branch
ORIGINAL_BRANCH=$(git branch --show-current)

# Get list of commits from source branch (oldest first, excluding initial commit)
COMMITS=$(git log --reverse --format="%H" "$SOURCE_BRANCH" | tail -n +2)
INIT_COMMIT=$(git log --reverse --format="%H" "$SOURCE_BRANCH" | head -1)

echo -e "${YELLOW}Found commits to process:${NC}"
git log --reverse --oneline "$SOURCE_BRANCH" | tail -n +2
echo ""

# Create orphan target branch from init commit
echo -e "${GREEN}Creating target branch $TARGET_BRANCH...${NC}"
git checkout "$SOURCE_BRANCH"
git checkout "$INIT_COMMIT"
git checkout --orphan "$TARGET_BRANCH"
git commit --allow-empty -m "Initialize metadata branch (v1)"
git checkout "$SOURCE_BRANCH"

# Process each commit
for COMMIT in $COMMITS; do
    COMMIT_MSG=$(git log -1 --format="%s" "$COMMIT")
    echo -e "${GREEN}Processing commit: $COMMIT_MSG${NC}"

    # Checkout source commit in temp worktree
    TEMP_DIR=$(mktemp -d)
    git worktree add --detach "$TEMP_DIR" "$COMMIT" 2>/dev/null

    # Checkout target branch
    git checkout "$TARGET_BRANCH"

    # Track which checkpoint directories we process
    PROCESSED_DIRS=""

    # Find all checkpoint directories (pattern: XX/YYYYYYYY/)
    cd "$TEMP_DIR"
    CHECKPOINT_DIRS=$(find . -maxdepth 2 -mindepth 2 -type d | grep -E '^\./[0-9a-f]{2}/[0-9a-f]+$' || true)

    for CHECKPOINT_PATH in $CHECKPOINT_DIRS; do
        CHECKPOINT_DIR="${CHECKPOINT_PATH#./}"
        echo "  Processing checkpoint: $CHECKPOINT_DIR"

        # Track this directory for git add later
        PROCESSED_DIRS="$PROCESSED_DIRS $CHECKPOINT_DIR"

        # Create checkpoint dir in target if not exists
        mkdir -p "$OLDPWD/$CHECKPOINT_DIR"

        # Check if root has session files (metadata.json with session_id)
        if [[ -f "$CHECKPOINT_PATH/metadata.json" ]]; then
            ROOT_META="$CHECKPOINT_PATH/metadata.json"

            # Check if this is session metadata (has session_id) or already aggregated
            if jq -e '.session_id' "$ROOT_META" > /dev/null 2>&1; then
                # This is session metadata at root - needs migration

                # Find existing numbered subdirs
                EXISTING_SUBDIRS=$(find "$CHECKPOINT_PATH" -maxdepth 1 -mindepth 1 -type d -name '[0-9]*' | sort -t'/' -k3 -n -r || true)

                # Calculate next session number (renumber existing + 1 for root)
                NEXT_NUM=0

                # Renumber existing subdirs (in reverse to avoid conflicts)
                for SUBDIR in $EXISTING_SUBDIRS; do
                    OLD_NUM=$(basename "$SUBDIR")
                    NEW_NUM=$((OLD_NUM + 1))

                    # Copy to target with new number
                    mkdir -p "$OLDPWD/$CHECKPOINT_DIR/$NEW_NUM"
                    # Copy non-metadata files
                    for FILE in context.md prompt.txt content_hash.txt full.jsonl; do
                        if [[ -f "$SUBDIR/$FILE" ]]; then
                            cp "$SUBDIR/$FILE" "$OLDPWD/$CHECKPOINT_DIR/$NEW_NUM/"
                        fi
                    done
                    # Transform metadata.json: agents to string, remove session_id and session_count
                    if [[ -f "$SUBDIR/metadata.json" ]]; then
                        jq 'del(.session_ids, .session_count) | if .agents | type == "array" then .agents = .agents[0] else . end' \
                            "$SUBDIR/metadata.json" > "$OLDPWD/$CHECKPOINT_DIR/$NEW_NUM/metadata.json"
                    fi

                    if [[ $NEW_NUM -gt $NEXT_NUM ]]; then
                        NEXT_NUM=$NEW_NUM
                    fi
                done

                # Move root session files to /0
                mkdir -p "$OLDPWD/$CHECKPOINT_DIR/0"
                # Copy non-metadata files
                for FILE in context.md prompt.txt content_hash.txt full.jsonl; do
                    if [[ -f "$CHECKPOINT_PATH/$FILE" ]]; then
                        cp "$CHECKPOINT_PATH/$FILE" "$OLDPWD/$CHECKPOINT_DIR/0/"
                    fi
                done
                # Transform metadata.json: agents to string, remove session_id and session_count
                if [[ -f "$CHECKPOINT_PATH/metadata.json" ]]; then
                    jq 'del(.session_ids, .session_count) | if .agents | type == "array" then .agents = .agents[0] else . end' \
                        "$CHECKPOINT_PATH/metadata.json" > "$OLDPWD/$CHECKPOINT_DIR/0/metadata.json"
                fi

                # Calculate total sessions (NEXT_NUM is highest 0-based index, so count = NEXT_NUM + 1)
                TOTAL_SESSIONS=$((NEXT_NUM + 1))

                # Build sessions array and aggregate data
                SESSIONS_JSON="[]"
                FILES_TOUCHED="[]"
                CHECKPOINTS_COUNT=0
                INPUT_TOKENS=0
                CACHE_CREATION=0
                CACHE_READ=0
                OUTPUT_TOKENS=0
                API_CALLS=0
                EARLIEST_DATE=""

                for i in $(seq 0 $((TOTAL_SESSIONS - 1))); do
                    SESSION_DIR="$OLDPWD/$CHECKPOINT_DIR/$i"
                    if [[ -d "$SESSION_DIR" ]]; then
                        SESSION_META="$SESSION_DIR/metadata.json"

                        # Build session entry (paths are absolute from branch root)
                        SESSION_ENTRY=$(jq -n \
                            --arg meta "/$CHECKPOINT_DIR/$i/metadata.json" \
                            --arg transcript "/$CHECKPOINT_DIR/$i/full.jsonl" \
                            --arg context "/$CHECKPOINT_DIR/$i/context.md" \
                            --arg hash "/$CHECKPOINT_DIR/$i/content_hash.txt" \
                            --arg prompt "/$CHECKPOINT_DIR/$i/prompt.txt" \
                            '{metadata: $meta, transcript: $transcript, context: $context, content_hash: $hash, prompt: $prompt}')

                        SESSIONS_JSON=$(echo "$SESSIONS_JSON" | jq --argjson entry "$SESSION_ENTRY" '. + [$entry]')

                        # Aggregate from session metadata
                        if [[ -f "$SESSION_META" ]]; then
                            # Files touched (union)
                            SESSION_FILES=$(jq -r '.files_touched // []' "$SESSION_META")
                            FILES_TOUCHED=$(echo "$FILES_TOUCHED" "$SESSION_FILES" | jq -s 'add | unique')

                            # Checkpoints count (sum)
                            CHECKPOINTS_COUNT=$((CHECKPOINTS_COUNT + $(jq -r '.checkpoints_count // 0' "$SESSION_META")))

                            # Token usage (sum)
                            INPUT_TOKENS=$((INPUT_TOKENS + $(jq -r '.token_usage.input_tokens // 0' "$SESSION_META")))
                            CACHE_CREATION=$((CACHE_CREATION + $(jq -r '.token_usage.cache_creation_tokens // 0' "$SESSION_META")))
                            CACHE_READ=$((CACHE_READ + $(jq -r '.token_usage.cache_read_tokens // 0' "$SESSION_META")))
                            OUTPUT_TOKENS=$((OUTPUT_TOKENS + $(jq -r '.token_usage.output_tokens // 0' "$SESSION_META")))
                            API_CALLS=$((API_CALLS + $(jq -r '.token_usage.api_call_count // 0' "$SESSION_META")))

                        fi
                    fi
                done

                # Get base info from original root metadata
                CHECKPOINT_ID=$(jq -r '.checkpoint_id // ""' "$ROOT_META")
                STRATEGY=$(jq -r '.strategy // "manual-commit"' "$ROOT_META")
                BRANCH=$(jq -r '.branch // ""' "$ROOT_META")

                # Create aggregated metadata.json
                jq -n \
                    --arg checkpoint_id "$CHECKPOINT_ID" \
                    --arg strategy "$STRATEGY" \
                    --arg branch "$BRANCH" \
                    --argjson checkpoints_count "$CHECKPOINTS_COUNT" \
                    --argjson files_touched "$FILES_TOUCHED" \
                    --argjson sessions "$SESSIONS_JSON" \
                    --argjson input_tokens "$INPUT_TOKENS" \
                    --argjson cache_creation "$CACHE_CREATION" \
                    --argjson cache_read "$CACHE_READ" \
                    --argjson output_tokens "$OUTPUT_TOKENS" \
                    --argjson api_calls "$API_CALLS" \
                    '{
                        checkpoint_id: $checkpoint_id,
                        strategy: $strategy,
                        branch: $branch,
                        checkpoints_count: $checkpoints_count,
                        files_touched: $files_touched,
                        sessions: $sessions,
                        token_usage: {
                            input_tokens: $input_tokens,
                            cache_creation_tokens: $cache_creation,
                            cache_read_tokens: $cache_read,
                            output_tokens: $output_tokens,
                            api_call_count: $api_calls
                        }
                    }' > "$OLDPWD/$CHECKPOINT_DIR/metadata.json"

                echo "    Migrated: $TOTAL_SESSIONS session(s)"
            else
                # Already aggregated format - copy but still transform session metadata
                # Transform root metadata.json to have absolute paths in sessions array
                jq --arg prefix "/$CHECKPOINT_DIR" \
                    '.sessions = [.sessions[] | {
                        metadata: ($prefix + "/" + (.metadata | ltrimstr("/"))),
                        transcript: ($prefix + "/" + (.transcript | ltrimstr("/"))),
                        context: ($prefix + "/" + (.context | ltrimstr("/"))),
                        content_hash: ($prefix + "/" + (.content_hash | ltrimstr("/"))),
                        prompt: ($prefix + "/" + (.prompt | ltrimstr("/")))
                    }]' "$CHECKPOINT_PATH/metadata.json" > "$OLDPWD/$CHECKPOINT_DIR/metadata.json"

                # Copy and transform each session subdir's metadata.json
                for SUBDIR in $(find "$CHECKPOINT_PATH" -maxdepth 1 -mindepth 1 -type d -name '[0-9]*'); do
                    SUBDIR_NUM=$(basename "$SUBDIR")
                    mkdir -p "$OLDPWD/$CHECKPOINT_DIR/$SUBDIR_NUM"

                    # Copy non-metadata files
                    for FILE in context.md prompt.txt content_hash.txt full.jsonl; do
                        if [[ -f "$SUBDIR/$FILE" ]]; then
                            cp "$SUBDIR/$FILE" "$OLDPWD/$CHECKPOINT_DIR/$SUBDIR_NUM/"
                        fi
                    done

                    # Transform metadata.json
                    if [[ -f "$SUBDIR/metadata.json" ]]; then
                        jq 'del(.session_ids, .session_count) | if .agents | type == "array" then .agents = .agents[0] else . end' \
                            "$SUBDIR/metadata.json" > "$OLDPWD/$CHECKPOINT_DIR/$SUBDIR_NUM/metadata.json"
                    fi
                done
                echo "    Copied with session metadata transformed"
            fi
        fi
    done

    cd "$OLDPWD"

    # Cleanup worktree
    git worktree remove "$TEMP_DIR" --force 2>/dev/null || rm -rf "$TEMP_DIR"

    # Only add the specific checkpoint directories we processed
    for DIR in $PROCESSED_DIRS; do
        git add "$DIR"
    done

    # Commit changes
    if ! git diff --cached --quiet; then
        git commit -m "$COMMIT_MSG"
        echo -e "  ${GREEN}Committed${NC}"
    else
        echo -e "  ${YELLOW}No changes${NC}"
    fi
done

# Return to original branch
git checkout "$ORIGINAL_BRANCH" 2>/dev/null || git checkout main

echo ""
echo -e "${GREEN}=== Migration Complete ===${NC}"
echo "New branch: $TARGET_BRANCH"
echo ""
echo "To verify:"
echo "  git log $TARGET_BRANCH"
echo "  git show $TARGET_BRANCH:<checkpoint_path>/metadata.json"
