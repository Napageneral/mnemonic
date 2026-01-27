# Handoff - Live Memory Eval + iMessage Payloads

Status: active  
Last updated: 2026-01-27

## Goal

Use live testing only (no fixtures). Run small, focused live evals against `cortex.db`,
inspect episode encoding + prompts, fix payload issues before deeper graph work.

## What we changed (Cortex)

1) **Live-only workflow**
- Removed fixture harness files.
- Updated docs to live-only testing.

2) **verify-memory-live improvements**
- Run IDs are prefixed onto episode IDs for cleanup.
- Cleanup tool: `cmd/cleanup-live-eval`.
- Debug dumps (episode, prompts, responses) per episode.
- 90-minute time-gap chunking for iMessage (default).
- Sender labeling fixes for 1:1 iMessage:
  - Thread name uses other participant instead of phone number.
  - Inbound Unknown -> other participant.
- Relationship JSON parse repair fallback for invalid JSON.
- Encoding improvements (from user edits):
  - `content_types`, `metadata_json`, `reply_to`, membership, attachments pulled into episode.
  - Reactions render as reactions (not messages).
  - Membership events render as lines.
  - Attachments render with media/file hints.

3) **Docs**
- `docs/HANDOFF_MEMORY_TESTING.md` and `docs/MEMORY_SYSTEM_SPEC.md` updated.
- `docs/HANDOFF_CONTACT_PERSON_SPLIT.md` updated.

## What we verified (live)

- Single iMessage episode payloads can be dumped and inspected via debug dir.
- Coed Coven group episode payloads can be generated and reviewed.
- Cursor episodes can be huge (token blowups); we are deferring AI/cursor tests for now.

## In progress by another agent (do not duplicate)

**Membership events ingestion**
- Capture `group_action_type` + `other_handle` from chat.db.
- Emit membership events into Cortex.

**Reactions ingestion**
- Ensure reactions are stored as their own event type (not text).

**Attachment encoding**
- Ensure attachments are tagged and rendered properly.

Spec for this work lives in Eve:
`eve/docs/IMESSAGE_GROUP_MEMBERSHIP_REACTIONS_SPEC.md`

## How to run live eval (current)

Minimal single-episode (iMessage):
```bash
RUN_ID="live-eval-YYYYMMDD-HHMMSS"
DEBUG_DIR="/Users/tyler/Library/Application Support/Cortex/live-eval-debug"
go run ./cmd/verify-memory-live \
  -threads "imessage:+17072268448" \
  -episodes-per-thread 1 \
  -min-episode-events 1 \
  -imessage-gap-minutes 90 \
  -run-id "$RUN_ID" \
  -debug-dir "$DEBUG_DIR" \
  -verbose
```

Cleanup:
```bash
go run ./cmd/cleanup-live-eval --run-id live-eval-YYYYMMDD-HHMMSS --db /Users/tyler/Library/Application Support/Cortex/cortex.db
```

## Next steps (recommended)

1) Re-run fresh live tests focused on iMessage + email (ignore AI/cursor).
2) Inspect episode payloads and prompts before scaling up.
3) Validate membership/reaction/attachment ingestion once merged.
4) After payloads are solid, resume larger live evals for graph extraction issues.
