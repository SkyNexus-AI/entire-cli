#!/usr/bin/env bash
set -euo pipefail

# Post a message to a Slack thread using the chat.postMessage API.
# Requires SLACK_BOT_TOKEN, SLACK_CHANNEL, and SLACK_THREAD_TS env vars.

text="${1:?message is required}"
payload="$(jq -n \
  --arg channel "$SLACK_CHANNEL" \
  --arg thread_ts "$SLACK_THREAD_TS" \
  --arg text "$text" \
  '{channel: $channel, thread_ts: $thread_ts, text: $text}')"

if ! response="$(curl -fsS https://slack.com/api/chat.postMessage \
  -H "Authorization: Bearer ${SLACK_BOT_TOKEN}" \
  -H 'Content-type: application/json; charset=utf-8' \
  --data "$payload")"; then
  echo "warning: slack notification failed" >&2
  exit 0
fi

if ! jq -e '.ok == true' >/dev/null <<<"$response"; then
  echo "warning: slack notification returned non-ok response" >&2
fi
