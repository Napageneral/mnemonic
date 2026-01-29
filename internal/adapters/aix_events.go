package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Napageneral/mnemonic/internal/contacts"
	_ "modernc.org/sqlite"
)

// AixEventsAdapter syncs trimmed AI turns from AIX to the Events Ledger.
// Each turn becomes 2 events: one user message (consolidated) and one assistant response (final text only).
// This is for memory extraction - what the user saw and read.
type AixEventsAdapter struct {
	source string // cursor, codex, nexus, clawdbot, ...
	dbPath string
}

// NewAixEventsAdapter creates a new AIX events adapter for a given source.
func NewAixEventsAdapter(source string) (*AixEventsAdapter, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("aix-events adapter requires source (e.g. cursor)")
	}

	dbPath, err := DefaultAixDBPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("aix database not found at %s (run aix sync --all first): %w", dbPath, err)
	}

	return &AixEventsAdapter{
		source: source,
		dbPath: dbPath,
	}, nil
}

func (a *AixEventsAdapter) Name() string {
	return "aix-events-" + a.source
}

func (a *AixEventsAdapter) Sync(ctx context.Context, cortexDB *sql.DB, full bool) (SyncResult, error) {
	start := time.Now()
	var result SyncResult

	// Open aix database (read-only)
	aixDB, err := sql.Open("sqlite", "file:"+a.dbPath+"?mode=ro")
	if err != nil {
		return result, fmt.Errorf("failed to open aix database: %w", err)
	}
	defer aixDB.Close()
	_, _ = aixDB.Exec("PRAGMA busy_timeout = 5000")

	// Enable foreign keys on cortex DB
	if _, err := cortexDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return result, fmt.Errorf("failed to enable foreign keys: %w", err)
	}
	_, _ = cortexDB.Exec("PRAGMA busy_timeout = 5000")
	_, _ = cortexDB.Exec("PRAGMA journal_mode = WAL")
	_, _ = cortexDB.Exec("PRAGMA synchronous = NORMAL")
	if full {
		_, _ = cortexDB.Exec("PRAGMA synchronous = OFF")
		_, _ = cortexDB.Exec("PRAGMA temp_store = MEMORY")
		_, _ = cortexDB.Exec("PRAGMA cache_size = -200000")
	}
	_, _ = cortexDB.Exec("PRAGMA defer_foreign_keys = ON")

	// Get sync watermark
	var lastSync int64
	if !full {
		row := cortexDB.QueryRow("SELECT last_sync_at FROM sync_watermarks WHERE adapter = ?", a.Name())
		if err := row.Scan(&lastSync); err != nil && err != sql.ErrNoRows {
			return result, fmt.Errorf("failed to get sync watermark: %w", err)
		}
	}

	result.Perf = map[string]string{}

	// Look up me person if present (optional)
	var mePersonID string
	_ = cortexDB.QueryRow("SELECT id FROM persons WHERE is_me = 1 LIMIT 1").Scan(&mePersonID)

	// Ensure "me" has a contact endpoint for this source
	meContactID, err := a.ensureMeContact(cortexDB, mePersonID)
	if err != nil {
		return result, err
	}

	// Cache AI contacts per model
	aiByModel := make(map[string]string)

	// Pre-fetch models to create contacts outside transaction
	modelKeys, err := a.listModelsInWindow(aixDB, lastSync)
	if err != nil {
		return result, err
	}
	for _, mk := range modelKeys {
		contactID, _, err := a.getOrCreateAIContact(cortexDB, mk)
		if err != nil {
			return result, err
		}
		aiByModel[mk] = contactID
	}

	// Sync threads (sessions)
	threadsCreated, threadsUpdated, threadPerf, err := a.syncThreads(aixDB, cortexDB, lastSync, full)
	if err != nil {
		return result, fmt.Errorf("sync threads: %w", err)
	}
	result.ThreadsCreated = threadsCreated
	result.ThreadsUpdated = threadsUpdated
	for k, v := range threadPerf {
		result.Perf["threads_"+k] = v
	}

	// Sync trimmed turn events
	eventsCreated, eventsUpdated, maxTS, eventPerf, err := a.syncTrimmedTurns(ctx, aixDB, cortexDB, lastSync, full, meContactID, aiByModel)
	if err != nil {
		return result, fmt.Errorf("sync trimmed turns: %w", err)
	}
	result.EventsCreated = eventsCreated
	result.EventsUpdated = eventsUpdated
	for k, v := range eventPerf {
		result.Perf[k] = v
	}

	// Update watermark
	if maxTS > lastSync {
		_, err = cortexDB.Exec(`
			INSERT INTO sync_watermarks (adapter, last_sync_at, last_event_id)
			VALUES (?, ?, NULL)
			ON CONFLICT(adapter) DO UPDATE SET last_sync_at = excluded.last_sync_at
		`, a.Name(), maxTS)
		if err != nil {
			return result, fmt.Errorf("failed to update sync watermark: %w", err)
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}

func (a *AixEventsAdapter) ensureMeContact(cortexDB *sql.DB, mePersonID string) (string, error) {
	identifier := fmt.Sprintf("aix-events:%s:user", a.source)
	displayName := ""
	if mePersonID != "" {
		_ = cortexDB.QueryRow(`SELECT COALESCE(display_name, canonical_name) FROM persons WHERE id = ?`, mePersonID).Scan(&displayName)
	}
	contactID, _, err := contacts.GetOrCreateContact(cortexDB, "human", identifier, displayName, a.Name())
	if err != nil {
		return "", fmt.Errorf("upsert aix user contact: %w", err)
	}
	if mePersonID != "" {
		_ = contacts.EnsurePersonContactLink(cortexDB, mePersonID, contactID, "deterministic", 1.0)
	}
	return contactID, nil
}

func (a *AixEventsAdapter) getOrCreateAIContact(cortexDB *sql.DB, modelKey string) (string, bool, error) {
	identifier := fmt.Sprintf("aix-events:%s:model:%s", a.source, modelKey)
	sourceTitle := strings.ToUpper(a.source[:1]) + a.source[1:]
	displayName := fmt.Sprintf("%s AI (%s)", sourceTitle, modelKey)
	return contacts.GetOrCreateContact(cortexDB, "ai", identifier, displayName, a.Name())
}

func (a *AixEventsAdapter) listModelsInWindow(aixDB *sql.DB, lastSyncSeconds int64) ([]string, error) {
	query := `
		SELECT DISTINCT COALESCE(NULLIF(TRIM(s.model), ''), 'unknown') as model_key
		FROM turns t
		JOIN sessions s ON t.session_id = s.id
		WHERE s.source = ?
		  AND CAST(COALESCE(t.timestamp, 0) / 1000 AS INTEGER) > ?
	`
	rows, err := aixDB.Query(query, a.source, lastSyncSeconds)
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
	return out, nil
}

func (a *AixEventsAdapter) syncThreads(aixDB, cortexDB *sql.DB, lastSync int64, full bool) (created, updated int, perf map[string]string, err error) {
	perf = map[string]string{}

	query := `
		SELECT id, created_at, model
		FROM sessions
		WHERE source = ?
	`
	args := []interface{}{a.source}
	if !full {
		query += " AND CAST(COALESCE(created_at, 0) / 1000 AS INTEGER) > ?"
		args = append(args, lastSync)
	}
	query += " ORDER BY created_at ASC"

	qStart := time.Now()
	rows, err := aixDB.Query(query, args...)
	if err != nil {
		return 0, 0, perf, fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()
	perf["query"] = time.Since(qStart).String()

	txStart := time.Now()
	tx, err := cortexDB.Begin()
	if err != nil {
		return 0, 0, perf, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO threads (id, channel, name, source_adapter, source_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_adapter, source_id) DO UPDATE SET
			name = excluded.name,
			updated_at = excluded.updated_at
	`)
	if err != nil {
		return 0, 0, perf, fmt.Errorf("prepare stmt: %w", err)
	}
	defer stmt.Close()

	threadPrefix := "aix_turn_session:"

	for rows.Next() {
		var (
			sessionID string
			createdAt sql.NullInt64
			model     sql.NullString
		)
		if err := rows.Scan(&sessionID, &createdAt, &model); err != nil {
			return created, updated, perf, fmt.Errorf("scan session: %w", err)
		}

		threadID := threadPrefix + sessionID
		threadName := "AI Session"
		if model.Valid && strings.TrimSpace(model.String) != "" {
			threadName = strings.TrimSpace(model.String)
		}

		now := time.Now().Unix()
		if createdAt.Valid && createdAt.Int64 > 0 {
			now = createdAt.Int64 / 1000
		}

		res, err := stmt.Exec(
			threadID,
			a.source,
			threadName,
			a.Name(),
			sessionID,
			now,
			now,
		)
		if err != nil {
			return created, updated, perf, fmt.Errorf("upsert thread: %w", err)
		}

		if n, _ := res.RowsAffected(); n > 0 {
			created++
		}
	}

	if err := rows.Err(); err != nil {
		return created, updated, perf, err
	}
	if err := tx.Commit(); err != nil {
		return created, updated, perf, fmt.Errorf("commit: %w", err)
	}
	perf["tx"] = time.Since(txStart).String()
	return created, updated, perf, nil
}

func (a *AixEventsAdapter) syncTrimmedTurns(
	ctx context.Context,
	aixDB *sql.DB,
	cortexDB *sql.DB,
	lastSync int64,
	full bool,
	meContactID string,
	aiByModel map[string]string,
) (created, updated int, maxTS int64, perf map[string]string, err error) {
	_ = ctx
	perf = map[string]string{}

	// Query turns with their query messages and response message
	query := `
		SELECT
			t.id as turn_id,
			t.session_id,
			t.query_message_ids,
			t.response_message_id,
			CAST(COALESCE(t.timestamp, 0) / 1000 AS INTEGER) as ts_sec,
			s.model
		FROM turns t
		JOIN sessions s ON t.session_id = s.id
		WHERE s.source = ?
	`
	args := []interface{}{a.source}
	if !full {
		query += " AND CAST(COALESCE(t.timestamp, 0) / 1000 AS INTEGER) > ?"
		args = append(args, lastSync)
	}
	query += " ORDER BY ts_sec ASC, t.id ASC"

	qStart := time.Now()
	rows, err := aixDB.Query(query, args...)
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("query turns: %w", err)
	}
	defer rows.Close()
	perf["turns_query"] = time.Since(qStart).String()

	txStart := time.Now()
	tx, err := cortexDB.Begin()
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmtInsertEvent, err := tx.Prepare(`
		INSERT INTO events (
			id, timestamp, channel, content_types, content,
			direction, thread_id, reply_to, source_adapter, source_id, metadata_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			content = excluded.content,
			metadata_json = excluded.metadata_json
	`)
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("prepare insert event: %w", err)
	}
	defer stmtInsertEvent.Close()

	stmtInsertParticipant, err := tx.Prepare(`
		INSERT OR IGNORE INTO event_participants (event_id, contact_id, role)
		VALUES (?, ?, ?)
	`)
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("prepare insert participant: %w", err)
	}
	defer stmtInsertParticipant.Close()

	const contentTypesText = `["text"]`
	threadPrefix := "aix_turn_session:"
	adapterPrefix := a.Name() + ":"

	for rows.Next() {
		var (
			turnID            string
			sessionID         string
			queryMessageIDs   sql.NullString
			responseMessageID sql.NullString
			tsSec             int64
			model             sql.NullString
		)
		if err := rows.Scan(&turnID, &sessionID, &queryMessageIDs, &responseMessageID, &tsSec, &model); err != nil {
			return created, updated, maxTS, perf, fmt.Errorf("scan turn: %w", err)
		}

		if tsSec > maxTS {
			maxTS = tsSec
		}

		modelKey := "unknown"
		if model.Valid && strings.TrimSpace(model.String) != "" {
			modelKey = strings.TrimSpace(model.String)
		}
		aiContactID := aiByModel[modelKey]
		if aiContactID == "" {
			aiContactID = aiByModel["unknown"]
		}

		threadID := threadPrefix + sessionID

		// --- User Event (consolidated query messages) ---
		userContent := ""
		if queryMessageIDs.Valid && queryMessageIDs.String != "" {
			userContent, err = a.consolidateQueryMessages(aixDB, queryMessageIDs.String)
			if err != nil {
				return created, updated, maxTS, perf, fmt.Errorf("consolidate user messages: %w", err)
			}
		}

		if strings.TrimSpace(userContent) != "" {
			userEventID := adapterPrefix + turnID + ":user"
			userSourceID := turnID + ":user"

			res, err := stmtInsertEvent.Exec(
				userEventID, tsSec, a.source, contentTypesText, userContent,
				"sent", threadID, a.Name(), userSourceID, nil,
			)
			if err != nil {
				return created, updated, maxTS, perf, fmt.Errorf("insert user event: %w", err)
			}
			if n, _ := res.RowsAffected(); n == 1 {
				created++
				// Participants
				if meContactID != "" {
					_, _ = stmtInsertParticipant.Exec(userEventID, meContactID, "sender")
				}
				if aiContactID != "" {
					_, _ = stmtInsertParticipant.Exec(userEventID, aiContactID, "recipient")
				}
			}
		}

		// --- Assistant Event (final response text only) ---
		assistantContent := ""
		if responseMessageID.Valid && responseMessageID.String != "" {
			assistantContent, err = a.extractFinalResponseText(aixDB, responseMessageID.String)
			if err != nil {
				return created, updated, maxTS, perf, fmt.Errorf("extract assistant response: %w", err)
			}
		}

		if strings.TrimSpace(assistantContent) != "" {
			assistantEventID := adapterPrefix + turnID + ":assistant"
			assistantSourceID := turnID + ":assistant"

			// Create metadata with model info
			metadataJSON := fmt.Sprintf(`{"model":"%s","turn_id":"%s"}`, modelKey, turnID)

			res, err := stmtInsertEvent.Exec(
				assistantEventID, tsSec, a.source, contentTypesText, assistantContent,
				"received", threadID, a.Name(), assistantSourceID, metadataJSON,
			)
			if err != nil {
				return created, updated, maxTS, perf, fmt.Errorf("insert assistant event: %w", err)
			}
			if n, _ := res.RowsAffected(); n == 1 {
				created++
				// Participants
				if aiContactID != "" {
					_, _ = stmtInsertParticipant.Exec(assistantEventID, aiContactID, "sender")
				}
				if meContactID != "" {
					_, _ = stmtInsertParticipant.Exec(assistantEventID, meContactID, "recipient")
				}
			}
		}
	}

	if err := rows.Err(); err != nil {
		return created, updated, maxTS, perf, err
	}
	if err := tx.Commit(); err != nil {
		return created, updated, maxTS, perf, fmt.Errorf("commit: %w", err)
	}
	perf["tx"] = time.Since(txStart).String()
	return created, updated, maxTS, perf, nil
}

// consolidateQueryMessages fetches user messages by IDs and combines their content
func (a *AixEventsAdapter) consolidateQueryMessages(aixDB *sql.DB, queryMessageIDsJSON string) (string, error) {
	var messageIDs []string
	if err := json.Unmarshal([]byte(queryMessageIDsJSON), &messageIDs); err != nil {
		// Might be a single ID without array brackets
		messageIDs = []string{queryMessageIDsJSON}
	}

	if len(messageIDs) == 0 {
		return "", nil
	}

	// Build query with placeholders
	placeholders := make([]string, len(messageIDs))
	args := make([]interface{}, len(messageIDs))
	for i, id := range messageIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(`
		SELECT content FROM messages
		WHERE id IN (%s) AND role = 'user'
		ORDER BY sequence ASC, id ASC
	`, strings.Join(placeholders, ","))

	rows, err := aixDB.Query(query, args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var contents []string
	for rows.Next() {
		var content sql.NullString
		if err := rows.Scan(&content); err != nil {
			return "", err
		}
		if content.Valid && strings.TrimSpace(content.String) != "" {
			contents = append(contents, strings.TrimSpace(content.String))
		}
	}

	return strings.Join(contents, "\n\n"), rows.Err()
}

// extractFinalResponseText gets the assistant message and strips tool calls/thinking
func (a *AixEventsAdapter) extractFinalResponseText(aixDB *sql.DB, responseMessageID string) (string, error) {
	var content sql.NullString
	err := aixDB.QueryRow(`
		SELECT content FROM messages WHERE id = ?
	`, responseMessageID).Scan(&content)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}

	if !content.Valid {
		return "", nil
	}

	// Strip tool call blocks and thinking blocks
	return stripToolAndThinkingBlocks(content.String), nil
}

// stripToolAndThinkingBlocks removes tool call XML blocks and thinking blocks from content.

// stripToolAndThinkingBlocks removes tool call blocks and thinking blocks from content.
// This extracts only the final text response the user would have seen.
func stripToolAndThinkingBlocks(content string) string {
	result := content

	// Remove XML-style tool call blocks using regex
	// Matches patterns like <function_calls>...</function_results>
	toolCallRe := regexp.MustCompile(`(?s)<[a-z_]+_calls>.*?</[a-z_]+_results>`)
	result = toolCallRe.ReplaceAllString(result, "")

	// Remove thinking blocks
	thinkingRe := regexp.MustCompile(`(?s)<thinking>.*?</thinking>`)
	result = thinkingRe.ReplaceAllString(result, "")

	// Remove antml blocks (Anthropic namespace)
	antmlRe := regexp.MustCompile(`(?s)<[^>]+>.*?</[^>]+>`)
	result = antmlRe.ReplaceAllString(result, "")

	// Trim excess whitespace
	result = strings.TrimSpace(result)

	// Collapse multiple newlines
	multiNewlineRe := regexp.MustCompile(`\n{3,}`)
	result = multiNewlineRe.ReplaceAllString(result, "\n\n")

	return result
}
