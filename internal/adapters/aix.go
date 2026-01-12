package adapters

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// AixAdapter syncs AI session events from aix's SQLite database (Cursor sessions, etc.)
type AixAdapter struct {
	source string // cursor, codex, opencode, ...
	dbPath string
}

// NewAixAdapter creates a new Aix adapter for a given source.
// Currently supported: source="cursor" (others can be added later).
func NewAixAdapter(source string) (*AixAdapter, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("aix adapter requires source (e.g. cursor)")
	}

	dbPath, err := defaultAixDBPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("aix database not found at %s (run aix sync --all first): %w", dbPath, err)
	}

	return &AixAdapter{
		source: source,
		dbPath: dbPath,
	}, nil
}

func (a *AixAdapter) Name() string {
	// Keep this stable + human friendly; also used as source_adapter and watermark key.
	// If we add more AI sources later, they'll get their own adapter names (codex, opencode, etc.).
	return a.source
}

func (a *AixAdapter) Sync(ctx context.Context, commsDB *sql.DB, full bool) (SyncResult, error) {
	start := time.Now()
	var result SyncResult

	// Open aix database (read-only)
	aixDB, err := sql.Open("sqlite", "file:"+a.dbPath+"?mode=ro")
	if err != nil {
		return result, fmt.Errorf("failed to open aix database: %w", err)
	}
	defer aixDB.Close()

	// Enable foreign keys on comms DB
	if _, err := commsDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return result, fmt.Errorf("failed to enable foreign keys: %w", err)
	}
	// Avoid transient SQLITE_BUSY errors during large writes.
	_, _ = commsDB.Exec("PRAGMA busy_timeout = 5000")
	// Prefer WAL for ingestion performance (safe default for SQLite).
	_, _ = commsDB.Exec("PRAGMA journal_mode = WAL")
	_, _ = commsDB.Exec("PRAGMA synchronous = NORMAL")
	// Aggressive full-sync pragmas (speed > durability during import).
	// NOTE: If the machine crashes mid-import, the comms DB could be left in a bad state.
	// For full rebuilds, this is an acceptable tradeoff for performance.
	if full {
		_, _ = commsDB.Exec("PRAGMA synchronous = OFF")
		_, _ = commsDB.Exec("PRAGMA temp_store = MEMORY")
		_, _ = commsDB.Exec("PRAGMA cache_size = -200000")        // ~200MB
		_, _ = commsDB.Exec("PRAGMA mmap_size = 268435456")       // 256MB
		_, _ = commsDB.Exec("PRAGMA wal_autocheckpoint = 1000000") // reduce checkpoints
	}
	// Keep correctness while reducing per-statement overhead.
	_, _ = commsDB.Exec("PRAGMA defer_foreign_keys = ON")

	// Get sync watermark (seconds)
	var lastSync int64
	var lastEventID sql.NullString
	if !full {
		row := commsDB.QueryRow("SELECT last_sync_at, last_event_id FROM sync_watermarks WHERE adapter = ?", a.Name())
		if err := row.Scan(&lastSync, &lastEventID); err != nil && err != sql.ErrNoRows {
			return result, fmt.Errorf("failed to get sync watermark: %w", err)
		}
	}

	// Look up me person if present (optional).
	var mePersonID string
	_ = commsDB.QueryRow("SELECT id FROM persons WHERE is_me = 1 LIMIT 1").Scan(&mePersonID)

	// Ensure "me" has an identity that indicates presence on this source (helps cross-platform views).
	if mePersonID != "" {
		if err := a.ensureMeIdentity(commsDB, mePersonID); err != nil {
			return result, err
		}
	}

	// Cache AI persons per model to avoid repeated DB round-trips.
	aiByModel := make(map[string]string) // modelKey -> personID

	lastEvent := ""
	if lastEventID.Valid {
		lastEvent = lastEventID.String
	}

	// Create AI persons *before* the big write transaction to avoid SQLITE_BUSY from nested transactions.
	modelKeys, err := a.listModelsInWindow(aixDB, lastSync, lastEvent)
	if err != nil {
		return result, err
	}
	for _, mk := range modelKeys {
		if _, ok := aiByModel[mk]; ok {
			continue
		}
		pid, createdPerson, err := a.getOrCreateAIPerson(commsDB, mk)
		if err != nil {
			return result, err
		}
		aiByModel[mk] = pid
		if createdPerson {
			result.PersonsCreated++
		}
	}

	phaseStart := time.Now()
	eventsCreated, eventsUpdated, maxTS, maxEventID, personsCreated, perf, err := a.syncMessages(ctx, aixDB, commsDB, lastSync, lastEvent, mePersonID, aiByModel)
	if err != nil {
		return result, err
	}
	result.EventsCreated = eventsCreated
	result.EventsUpdated = eventsUpdated
	result.PersonsCreated += personsCreated
	if result.Perf == nil {
		result.Perf = map[string]string{}
	}
	for k, v := range perf {
		result.Perf[k] = v
	}
	result.Perf["total"] = time.Since(phaseStart).String()

	// Update watermark to max imported event timestamp (seconds)
	watermark := lastSync
	if maxTS > watermark {
		watermark = maxTS
	}
	newLastEventID := lastEvent
	if maxTS > lastSync {
		newLastEventID = maxEventID
	} else if maxTS == lastSync && maxEventID != "" && maxEventID > lastEvent {
		newLastEventID = maxEventID
	}
	_, err = commsDB.Exec(`
		INSERT INTO sync_watermarks (adapter, last_sync_at, last_event_id)
		VALUES (?, ?, ?)
		ON CONFLICT(adapter) DO UPDATE SET
			last_sync_at = excluded.last_sync_at,
			last_event_id = excluded.last_event_id
	`, a.Name(), watermark, nullIfEmpty(newLastEventID))
	if err != nil {
		return result, fmt.Errorf("failed to update sync watermark: %w", err)
	}

	result.Duration = time.Since(start)
	return result, nil
}

func (a *AixAdapter) listModelsInWindow(aixDB *sql.DB, lastSyncSeconds int64, lastEventID string) ([]string, error) {
	query := `
		SELECT DISTINCT COALESCE(NULLIF(TRIM(s.model), ''), 'unknown') as model_key
		FROM messages m
		JOIN sessions s ON m.session_id = s.id
		WHERE s.source = ?
		  AND (
		    CAST(COALESCE(m.timestamp, s.created_at) / 1000 AS INTEGER) > ?
		    OR (CAST(COALESCE(m.timestamp, s.created_at) / 1000 AS INTEGER) = ? AND m.id > ?)
		  )
	`
	rows, err := aixDB.Query(query, a.source, lastSyncSeconds, lastSyncSeconds, lastEventID)
	if err != nil {
		return nil, fmt.Errorf("failed to list models: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var mk string
		if err := rows.Scan(&mk); err != nil {
			return nil, fmt.Errorf("scan model: %w", err)
		}
		out = append(out, mk)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return []string{"unknown"}, nil
	}
	// Ensure fallback exists.
	foundUnknown := false
	for _, mk := range out {
		if mk == "unknown" {
			foundUnknown = true
			break
		}
	}
	if !foundUnknown {
		out = append(out, "unknown")
	}
	return out, nil
}

func nullIfEmpty(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func (a *AixAdapter) ensureMeIdentity(commsDB *sql.DB, mePersonID string) error {
	// This is intentionally coarse; Eve whoami is the canonical rich identity seed.
	identityChannel := "aix"
	identityIdentifier := fmt.Sprintf("aix:%s:user", a.source)
	now := time.Now().Unix()
	identityID := uuid.New().String()
	_, err := commsDB.Exec(`
		INSERT INTO identities (id, person_id, channel, identifier, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(channel, identifier) DO UPDATE SET person_id = excluded.person_id
	`, identityID, mePersonID, identityChannel, identityIdentifier, now)
	if err != nil {
		return fmt.Errorf("upsert me aix identity: %w", err)
	}
	return nil
}

func (a *AixAdapter) getOrCreateAIPerson(commsDB *sql.DB, modelKey string) (personID string, created bool, err error) {
	// Map each (source, model) to a stable identity key.
	identityChannel := "ai"
	identityIdentifier := fmt.Sprintf("aix:%s:model:%s", a.source, modelKey)

	// Try lookup first
	row := commsDB.QueryRow(`SELECT person_id FROM identities WHERE channel = ? AND identifier = ?`, identityChannel, identityIdentifier)
	if err := row.Scan(&personID); err == nil {
		return personID, false, nil
	} else if err != sql.ErrNoRows {
		return "", false, fmt.Errorf("failed to query ai identity: %w", err)
	}

	// Create person + identity
	personID = uuid.New().String()
	now := time.Now().Unix()
	canonicalName := "AI Assistant"
	sourceTitle := strings.ToUpper(a.source[:1]) + a.source[1:]
	displayName := fmt.Sprintf("%s AI (%s)", sourceTitle, modelKey)

	tx, err := commsDB.Begin()
	if err != nil {
		return "", false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO persons (id, canonical_name, display_name, is_me, created_at, updated_at)
		VALUES (?, ?, ?, 0, ?, ?)
	`, personID, canonicalName, displayName, now, now)
	if err != nil {
		return "", false, fmt.Errorf("insert ai person: %w", err)
	}

	identityID := uuid.New().String()
	_, err = tx.Exec(`
		INSERT INTO identities (id, person_id, channel, identifier, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(channel, identifier) DO NOTHING
	`, identityID, personID, identityChannel, identityIdentifier, now)
	if err != nil {
		return "", false, fmt.Errorf("insert ai identity: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", false, fmt.Errorf("commit tx: %w", err)
	}
	return personID, true, nil
}

func (a *AixAdapter) syncMessages(
	ctx context.Context,
	aixDB *sql.DB,
	commsDB *sql.DB,
	lastSyncSeconds int64,
	lastEventID string,
	mePersonID string,
	aiByModel map[string]string,
) (created int, updated int, maxImportedTS int64, maxImportedEventID string, personsCreated int, perf map[string]string, err error) {
	_ = ctx
	perf = map[string]string{}

	adapterPrefix := a.Name() + ":"
	threadPrefix := "aix_session:"
	const contentTypesText = "[\"text\"]"

	query := `
		SELECT
			m.id as message_id,
			m.session_id,
			m.role,
			m.content,
			CAST(COALESCE(m.timestamp, s.created_at) / 1000 AS INTEGER) as ts_sec,
			s.model
		FROM messages m
		JOIN sessions s ON m.session_id = s.id
		WHERE s.source = ?
		  AND (
		    CAST(COALESCE(m.timestamp, s.created_at) / 1000 AS INTEGER) > ?
		    OR (CAST(COALESCE(m.timestamp, s.created_at) / 1000 AS INTEGER) = ? AND m.id > ?)
		  )
		ORDER BY ts_sec ASC, m.id ASC
	`

	qStart := time.Now()
	rows, err := aixDB.Query(query, a.source, lastSyncSeconds, lastSyncSeconds, lastEventID)
	if err != nil {
		return 0, 0, 0, "", 0, perf, fmt.Errorf("failed to query aix messages: %w", err)
	}
	defer rows.Close()
	perf["query"] = time.Since(qStart).String()

	// Bulk write in a single transaction for SQLite performance.
	txStart := time.Now()
	tx, err := commsDB.Begin()
	if err != nil {
		return 0, 0, 0, "", 0, perf, fmt.Errorf("begin comms tx: %w", err)
	}
	defer tx.Rollback()

	stmtInsertEvent, err := tx.Prepare(`
		INSERT OR IGNORE INTO events (
			id, timestamp, channel, content_types, content,
			direction, thread_id, reply_to, source_adapter, source_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?)
	`)
	if err != nil {
		return 0, 0, 0, "", 0, perf, fmt.Errorf("prepare insert event: %w", err)
	}
	defer stmtInsertEvent.Close()

	stmtUpdateEvent, err := tx.Prepare(`
		UPDATE events
		SET
			content = ?,
			content_types = ?,
			thread_id = ?
		WHERE source_adapter = ?
		  AND source_id = ?
		  AND (
		    content IS NOT ?
		    OR content_types IS NOT ?
		    OR thread_id IS NOT ?
		  )
	`)
	if err != nil {
		return 0, 0, 0, "", 0, perf, fmt.Errorf("prepare update event: %w", err)
	}
	defer stmtUpdateEvent.Close()

	stmtInsertParticipant, err := tx.Prepare(`
		INSERT OR IGNORE INTO event_participants (event_id, person_id, role)
		VALUES (?, ?, ?)
	`)
	if err != nil {
		return 0, 0, 0, "", 0, perf, fmt.Errorf("prepare insert participant: %w", err)
	}
	defer stmtInsertParticipant.Close()

	for rows.Next() {
		var (
			messageID string
			sessionID string
			role      string
			content   sql.NullString
			tsSec     int64
			model     sql.NullString
		)
		if err := rows.Scan(&messageID, &sessionID, &role, &content, &tsSec, &model); err != nil {
			return created, updated, maxImportedTS, maxImportedEventID, personsCreated, perf, fmt.Errorf("scan aix message: %w", err)
		}
		if tsSec > maxImportedTS {
			maxImportedTS = tsSec
			maxImportedEventID = messageID
		} else if tsSec == maxImportedTS && messageID > maxImportedEventID {
			maxImportedEventID = messageID
		}

		modelKey := "unknown"
		if model.Valid && strings.TrimSpace(model.String) != "" {
			modelKey = strings.TrimSpace(model.String)
		}

		aiPersonID, ok := aiByModel[modelKey]
		if !ok {
			// Should have been prefetched; fall back to "unknown" if needed.
			aiPersonID = aiByModel["unknown"]
		}

		// Map to comms event semantics
		direction := "observed"
		switch role {
		case "user":
			direction = "sent"
		case "assistant":
			direction = "received"
		case "tool":
			direction = "observed"
		}

		threadID := threadPrefix + sessionID

		// Deterministic event ID to avoid UUID cost and extra lookups.
		eventID := adapterPrefix + messageID

		// Insert if new.
		res, err := stmtInsertEvent.Exec(eventID, tsSec, "cursor", contentTypesText, content.String, direction, threadID, a.Name(), messageID)
		if err != nil {
			return created, updated, maxImportedTS, maxImportedEventID, personsCreated, perf, fmt.Errorf("insert event: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 1 {
			created++
		} else {
			// Update only if content/thread changed (prevents massive write churn on incremental runs).
			res2, err := stmtUpdateEvent.Exec(
				content.String, contentTypesText, threadID,
				a.Name(), messageID,
				content.String, contentTypesText, threadID,
			)
			if err != nil {
				return created, updated, maxImportedTS, maxImportedEventID, personsCreated, perf, fmt.Errorf("update event: %w", err)
			}
			if n2, _ := res2.RowsAffected(); n2 == 1 {
				updated++
			}
		}

		// Participants
		if mePersonID != "" && aiPersonID != "" {
			switch role {
			case "user":
				_, _ = stmtInsertParticipant.Exec(eventID, mePersonID, "sender")
				_, _ = stmtInsertParticipant.Exec(eventID, aiPersonID, "recipient")
			case "assistant":
				_, _ = stmtInsertParticipant.Exec(eventID, aiPersonID, "sender")
				_, _ = stmtInsertParticipant.Exec(eventID, mePersonID, "recipient")
			default:
				_, _ = stmtInsertParticipant.Exec(eventID, mePersonID, "observer")
				_, _ = stmtInsertParticipant.Exec(eventID, aiPersonID, "observer")
			}
		}
	}

	if err := rows.Err(); err != nil {
		return created, updated, maxImportedTS, maxImportedEventID, personsCreated, perf, err
	}
	if err := tx.Commit(); err != nil {
		return created, updated, maxImportedTS, maxImportedEventID, personsCreated, perf, fmt.Errorf("commit comms tx: %w", err)
	}
	perf["tx_commit"] = time.Since(txStart).String()
	return created, updated, maxImportedTS, maxImportedEventID, personsCreated, perf, nil
}

func insertParticipant(db *sql.DB, eventID, personID, role string) error {
	_, err := db.Exec(`
		INSERT INTO event_participants (event_id, person_id, role)
		VALUES (?, ?, ?)
		ON CONFLICT(event_id, person_id, role) DO NOTHING
	`, eventID, personID, role)
	return err
}

func defaultAixDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "aix", "aix.db"), nil
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "aix", "aix.db"), nil
	}
	return filepath.Join(home, ".local", "share", "aix", "aix.db"), nil
}

