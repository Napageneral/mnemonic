# Memory Testing Handoff (Live-Only)

Goal: evaluate the memory system using **real data** in the primary `cortex.db` and make behavior improvements based on live results. Fixture-based tests are deprecated and removed from the workflow.

## 1) Context: what changed

- Contact/person split landed. `event_participants` now stores `contact_id` (not `person_id`).
- New tables: `contacts`, `contact_identifiers`, `person_contact_links`.
- Person resolution now goes through `person_contact_links` and uses `confidence` + `last_seen_at` ordering.
- Entity type name is `Organization` (compat alias: `Company`).

## 2) Live-only testing stack

Primary harness (live eval against `cortex.db`):
- `cmd/verify-memory-live/main.go`
- Writes memory extraction results into `cortex.db` memory tables.
- Uses a **run ID** prefix so results can be cleaned up later.

Cleanup tool (delete eval data by run ID):
- `cmd/cleanup-live-eval/main.go`

Unit tests:
- `go test ./internal/memory -count=1` (still useful for small pieces)

## 3) How to run live eval

Minimal run:
```bash
go run ./cmd/verify-memory-live -threads "imessage:+16508238440" -episodes-per-thread 5 -events-per-episode 50 -verbose
```

Large multi-thread run (recommended):
```bash
go run ./cmd/verify-memory-live \
  -threads "imessage:+17072268448,imessage:+16508238440,imessage:chat773521807676249821,<gmail-thread>,<cursor-session>" \
  -episodes-per-thread 10 \
  -events-per-episode 10 \
  -min-episode-events 5 \
  -imessage-gap-minutes 90 \
  -run-id live-eval-YYYYMMDD-HHMMSS \
  -verbose
```

Notes:
- The default output DB is the primary `cortex.db`.
- Episodes are tagged as `runID:threadID-epN` for cleanup.
- iMessage uses a 90-minute time-gap window to define episodes.
- Use small `events-per-episode` (10) for Gmail threads, which are shorter.

## 4) Cleanup after review

```bash
go run ./cmd/cleanup-live-eval --run-id live-eval-YYYYMMDD-HHMMSS --db /Users/tyler/Library/Application Support/Cortex/cortex.db
```

Dry run (counts only):
```bash
go run ./cmd/cleanup-live-eval --run-id live-eval-YYYYMMDD-HHMMSS --dry-run
```

## 5) Contact/person split impact (where to look)

Likely sources of regressions:
- `internal/query/query.go` (participant lookup)
- `internal/compute/engine.go` (episode text / masked)
- `internal/timeline/timeline.go`
- `internal/identify/*` (list/search/merge/suggestions)
- `cmd/verify-memory-live/main.go` (live harness joins)

Adapters now insert `contact_id` into `event_participants`:
- `internal/adapters/{gmail,eve,aix,bird,calendar,imessage}.go`
- `internal/importer/mbox.go`

## 6) Known risk areas to verify

1) Person resolution ambiguity  
   - Multiple `person_contact_links` can exist per contact.  
   - Queries select one via highest confidence + most recent.

2) Unlinked contacts  
   - Contacts without a person link are valid.  
   - Ensure display name fallback is acceptable in UI and memory outputs.

3) Event counts per person  
   - Now calculated through `person_contact_links` + `event_participants`.  
   - Verify counts match expectations.

4) Shared contacts  
   - Family phone or shared inbox can map to multiple persons.  
   - Ensure resolution is consistent and does not mis-attribute entities.

## 7) Quick sanity queries

```sql
-- Should be 0
SELECT COUNT(*) FROM event_participants WHERE contact_id IS NULL;

-- Count contacts without person links (expected > 0)
SELECT COUNT(*) FROM contacts c
LEFT JOIN person_contact_links pcl ON c.id = pcl.contact_id
WHERE pcl.contact_id IS NULL;
```

