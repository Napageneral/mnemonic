package adapters

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// EveAdapter syncs iMessage events from Eve's database
type EveAdapter struct {
	eveDBPath string
}

// NewEveAdapter creates a new Eve adapter
func NewEveAdapter() (*EveAdapter, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	eveDBPath := filepath.Join(home, "Library", "Application Support", "Eve", "eve.db")
	if _, err := os.Stat(eveDBPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("Eve database not found at %s", eveDBPath)
	}

	return &EveAdapter{
		eveDBPath: eveDBPath,
	}, nil
}

func (e *EveAdapter) Name() string {
	return "imessage"
}

func (e *EveAdapter) Sync(ctx context.Context, commsDB *sql.DB, full bool) (SyncResult, error) {
	startTime := time.Now()
	result := SyncResult{}

	// Open Eve database (read-only)
	eveDB, err := sql.Open("sqlite", "file:"+e.eveDBPath+"?mode=ro&_busy_timeout=5000")
	if err != nil {
		return result, fmt.Errorf("failed to open Eve database: %w", err)
	}
	defer eveDB.Close()

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

	// Get sync watermark (last synced timestamp)
	var lastSyncTimestamp int64
	if !full {
		row := commsDB.QueryRow("SELECT last_sync_at FROM sync_watermarks WHERE adapter = ?", e.Name())
		if err := row.Scan(&lastSyncTimestamp); err != nil && err != sql.ErrNoRows {
			return result, fmt.Errorf("failed to get sync watermark: %w", err)
		}
	}

	// Sync contacts first (to establish person/identity mappings)
	contactsStart := time.Now()
	personsCreated, contactMap, mePersonID, perfContacts, err := e.syncContacts(ctx, eveDB, commsDB)
	if err != nil {
		return result, fmt.Errorf("failed to sync contacts: %w", err)
	}
	result.PersonsCreated = personsCreated
	if result.Perf == nil {
		result.Perf = map[string]string{}
	}
	for k, v := range perfContacts {
		result.Perf["contacts."+k] = v
	}
	result.Perf["contacts.total"] = time.Since(contactsStart).String()

	// Sync messages
	messagesStart := time.Now()
	eventsCreated, eventsUpdated, maxImportedTimestamp, perfMessages, err := e.syncMessages(ctx, eveDB, commsDB, lastSyncTimestamp, contactMap, mePersonID)
	if err != nil {
		return result, fmt.Errorf("failed to sync messages: %w", err)
	}
	result.EventsCreated = eventsCreated
	result.EventsUpdated = eventsUpdated
	for k, v := range perfMessages {
		result.Perf["messages."+k] = v
	}
	result.Perf["messages.total"] = time.Since(messagesStart).String()

	// Update sync watermark
	// IMPORTANT: use the max imported event timestamp, NOT wall-clock time.
	// This avoids skipping late-arriving/backfilled messages whose timestamp is older than "now".
	watermark := lastSyncTimestamp
	if maxImportedTimestamp > watermark {
		watermark = maxImportedTimestamp
	}
	_, err = commsDB.Exec(`
		INSERT INTO sync_watermarks (adapter, last_sync_at)
		VALUES (?, ?)
		ON CONFLICT(adapter) DO UPDATE SET last_sync_at = excluded.last_sync_at
	`, e.Name(), watermark)
	if err != nil {
		return result, fmt.Errorf("failed to update sync watermark: %w", err)
	}

	result.Duration = time.Since(startTime)
	if result.Perf == nil {
		result.Perf = map[string]string{}
	}
	result.Perf["total"] = result.Duration.String()
	return result, nil
}

// syncContacts syncs Eve contacts to comms persons and identities
func (e *EveAdapter) syncContacts(ctx context.Context, eveDB, commsDB *sql.DB) (personsCreated int, contactMap map[int64]string, mePersonID string, perf map[string]string, err error) {
	perf = map[string]string{}

	// Seed "me" from Eve's rich whoami info (authoritative identity set: phones/emails/handles).
	// This is the key link that allows other adapters (aix/gmail/...) to attach to a cohesive user.
	wStart := time.Now()
	meCreated, meID, err := e.syncWhoami(ctx, eveDB, commsDB)
	if err != nil {
		return 0, nil, "", perf, err
	}
	mePersonID = meID
	perf["whoami"] = time.Since(wStart).String()

	// Query contacts and their identifiers from Eve
	qStart := time.Now()
	rows, err := eveDB.Query(`
		SELECT
			c.id,
			c.name,
			c.nickname,
			ci.identifier,
			ci.type
		FROM contacts c
		LEFT JOIN contact_identifiers ci ON c.id = ci.contact_id
		WHERE c.is_me = 0
		ORDER BY c.id
	`)
	if err != nil {
		return meCreated, nil, mePersonID, perf, fmt.Errorf("failed to query Eve contacts: %w", err)
	}
	defer rows.Close()
	perf["query"] = time.Since(qStart).String()

	personsCreated = meCreated
	contactMap = make(map[int64]string) // Eve contact_id -> comms person_id

	// Bulk write in a single transaction for SQLite performance.
	txStart := time.Now()
	tx, err := commsDB.Begin()
	if err != nil {
		return personsCreated, contactMap, mePersonID, perf, fmt.Errorf("begin comms tx: %w", err)
	}
	defer tx.Rollback()

	stmtInsertPerson, err := tx.Prepare(`
		INSERT INTO persons (id, canonical_name, is_me, created_at, updated_at)
		VALUES (?, ?, 0, ?, ?)
	`)
	if err != nil {
		return personsCreated, contactMap, mePersonID, perf, fmt.Errorf("prepare insert person: %w", err)
	}
	defer stmtInsertPerson.Close()

	stmtInsertIdentity, err := tx.Prepare(`
		INSERT INTO identities (id, person_id, channel, identifier, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(channel, identifier) DO NOTHING
	`)
	if err != nil {
		return personsCreated, contactMap, mePersonID, perf, fmt.Errorf("prepare insert identity: %w", err)
	}
	defer stmtInsertIdentity.Close()

	for rows.Next() {
		var eveContactID int64
		var name, nickname sql.NullString
		var identifier, identifierType sql.NullString

		if err := rows.Scan(&eveContactID, &name, &nickname, &identifier, &identifierType); err != nil {
			return personsCreated, contactMap, mePersonID, perf, fmt.Errorf("failed to scan contact row: %w", err)
		}

		// Determine canonical name
		canonicalName := "Unknown"
		if name.Valid && name.String != "" {
			canonicalName = name.String
		} else if nickname.Valid && nickname.String != "" {
			canonicalName = nickname.String
		} else if identifier.Valid && identifier.String != "" {
			canonicalName = identifier.String
		}

		// Check if person already exists (by identifier or by name)
		var personID string
		if identifier.Valid && identifierType.Valid {
			// Try to find person by identifier
			row := commsDB.QueryRow(`
				SELECT person_id FROM identities
				WHERE channel = ? AND identifier = ?
			`, identifierType.String, identifier.String)
			if err := row.Scan(&personID); err != nil && err != sql.ErrNoRows {
				return personsCreated, contactMap, mePersonID, perf, fmt.Errorf("failed to query identity: %w", err)
			}
		}

		// If not found, create new person
		if personID == "" {
			personID = uuid.New().String()
			now := time.Now().Unix()

			// Best-effort: if insert fails due to uniqueness collisions on identities, we still map this contact_id.
			if _, err := stmtInsertPerson.Exec(personID, canonicalName, now, now); err == nil {
				personsCreated++
			}
		}

		contactMap[eveContactID] = personID

		// Add identity if we have an identifier
		if identifier.Valid && identifierType.Valid {
			identityID := uuid.New().String()
			now := time.Now().Unix()

			_, _ = stmtInsertIdentity.Exec(identityID, personID, identifierType.String, identifier.String, now)
		}
	}

	if err := rows.Err(); err != nil {
		return personsCreated, contactMap, mePersonID, perf, err
	}
	if err := tx.Commit(); err != nil {
		return personsCreated, contactMap, mePersonID, perf, fmt.Errorf("commit comms tx: %w", err)
	}
	perf["tx_commit"] = time.Since(txStart).String()
	return personsCreated, contactMap, mePersonID, perf, nil
}

func (e *EveAdapter) syncWhoami(ctx context.Context, eveDB, commsDB *sql.DB) (personsCreated int, mePersonID string, err error) {
	type whoamiJSON struct {
		OK     bool     `json:"ok"`
		Name   string   `json:"name"`
		Emails []string `json:"emails"`
		Phones []string `json:"phones"`
	}

	findEveBin := func() (string, bool) {
		// Explicit override (best for testing + portability).
		if p := os.Getenv("COMMS_EVE_BIN"); p != "" {
			return p, true
		}
		if p := os.Getenv("EVE_BIN"); p != "" {
			return p, true
		}

		// Normal PATH lookup.
		if p, err := exec.LookPath("eve"); err == nil {
			return p, true
		}

		// Common dev locations in this Nexus workspace.
		home, err := os.UserHomeDir()
		if err == nil {
			candidates := []string{
				filepath.Join(home, "nexus", "home", "projects", "eve", "bin", "eve"),
				filepath.Join(home, "Desktop", "projects", "eve", "bin", "eve"),
			}
			for _, p := range candidates {
				if st, err := os.Stat(p); err == nil && !st.IsDir() {
					return p, true
				}
			}
		}

		return "", false
	}

	// Prefer the Eve CLI `whoami` output (this is what the user sees and is the richest signal).
	// If the `eve` binary is not available, fall back to any warehouse representation (if present).
	if evePath, ok := findEveBin(); ok {
		cmd := exec.CommandContext(ctx, evePath, "whoami")
		out, runErr := cmd.Output()
		if runErr != nil {
			return 0, "", fmt.Errorf("failed to run `eve whoami` for seeding me: %w", runErr)
		}
		var w whoamiJSON
		if err := json.Unmarshal(out, &w); err != nil {
			return 0, "", fmt.Errorf("failed to parse `eve whoami` output: %w", err)
		}
		if w.OK {
			now := time.Now().Unix()

			// Find or create the comms "me" person.
			_ = commsDB.QueryRow("SELECT id FROM persons WHERE is_me = 1 LIMIT 1").Scan(&mePersonID)

			bestName := w.Name
			if bestName == "" {
				bestName = "Me"
			}

			if mePersonID == "" {
				mePersonID = uuid.New().String()
				if _, err := commsDB.Exec(`
					INSERT INTO persons (id, canonical_name, is_me, created_at, updated_at)
					VALUES (?, ?, 1, ?, ?)
				`, mePersonID, bestName, now, now); err != nil {
					return 0, "", fmt.Errorf("failed to create me person: %w", err)
				}
				personsCreated++
			} else {
				// Best-effort: keep canonical_name fresh if it was a placeholder.
				_, _ = commsDB.Exec(
					`UPDATE persons SET canonical_name = ?, updated_at = ? WHERE id = ? AND (canonical_name = '' OR canonical_name = 'Me' OR canonical_name = 'Unknown')`,
					bestName, now, mePersonID,
				)
			}

			// Upsert phone/email identities onto the comms me person.
			for _, p := range w.Phones {
				if p == "" {
					continue
				}
				identityID := uuid.New().String()
				if _, err := commsDB.Exec(`
					INSERT INTO identities (id, person_id, channel, identifier, created_at)
					VALUES (?, ?, ?, ?, ?)
					ON CONFLICT(channel, identifier) DO UPDATE SET person_id = excluded.person_id
				`, identityID, mePersonID, "phone", p, now); err != nil {
					return personsCreated, mePersonID, fmt.Errorf("failed to upsert whoami phone identity: %w", err)
				}
			}
			for _, em := range w.Emails {
				if em == "" {
					continue
				}
				identityID := uuid.New().String()
				if _, err := commsDB.Exec(`
					INSERT INTO identities (id, person_id, channel, identifier, created_at)
					VALUES (?, ?, ?, ?, ?)
					ON CONFLICT(channel, identifier) DO UPDATE SET person_id = excluded.person_id
				`, identityID, mePersonID, "email", em, now); err != nil {
					return personsCreated, mePersonID, fmt.Errorf("failed to upsert whoami email identity: %w", err)
				}
			}

			return personsCreated, mePersonID, nil
		}
	}

	// Find or create the comms "me" person.
	_ = commsDB.QueryRow("SELECT id FROM persons WHERE is_me = 1 LIMIT 1").Scan(&mePersonID)

	rows, err := eveDB.Query(`
		SELECT
			c.name,
			c.nickname,
			ci.identifier,
			ci.type
		FROM contacts c
		LEFT JOIN contact_identifiers ci ON c.id = ci.contact_id
		WHERE c.is_me = 1
		ORDER BY c.id
	`)
	if err != nil {
		return 0, "", fmt.Errorf("failed to query Eve whoami: %w", err)
	}
	defer rows.Close()

	var (
		bestName        string
		identifiersSeen bool
	)

	type ident struct {
		channel    string
		identifier string
	}
	var idents []ident

	for rows.Next() {
		var name, nickname, identifier, identifierType sql.NullString
		if err := rows.Scan(&name, &nickname, &identifier, &identifierType); err != nil {
			return 0, "", fmt.Errorf("failed to scan Eve whoami row: %w", err)
		}

		// Determine best canonical name candidate.
		if bestName == "" {
			if name.Valid && name.String != "" {
				bestName = name.String
			} else if nickname.Valid && nickname.String != "" {
				bestName = nickname.String
			}
		}

		if identifier.Valid && identifierType.Valid && identifier.String != "" && identifierType.String != "" {
			identifiersSeen = true
			idents = append(idents, ident{
				channel:    identifierType.String,
				identifier: identifier.String,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return 0, "", fmt.Errorf("error iterating Eve whoami: %w", err)
	}

	// If Eve has no whoami rows, do nothing.
	if bestName == "" && !identifiersSeen {
		return 0, "", nil
	}
	if bestName == "" {
		bestName = "Me"
	}

	now := time.Now().Unix()
	if mePersonID == "" {
		mePersonID = uuid.New().String()
		if _, err := commsDB.Exec(`
			INSERT INTO persons (id, canonical_name, is_me, created_at, updated_at)
			VALUES (?, ?, 1, ?, ?)
		`, mePersonID, bestName, now, now); err != nil {
			return 0, "", fmt.Errorf("failed to create me person: %w", err)
		}
		personsCreated++
	} else {
		// Best-effort: keep canonical_name fresh if it was a placeholder.
		_, _ = commsDB.Exec(`UPDATE persons SET canonical_name = ?, updated_at = ? WHERE id = ? AND (canonical_name = '' OR canonical_name = 'Me' OR canonical_name = 'Unknown')`,
			bestName, now, mePersonID,
		)
	}

	// Upsert all whoami identifiers onto the comms me person.
	for _, it := range idents {
		identityID := uuid.New().String()
		_, err := commsDB.Exec(`
			INSERT INTO identities (id, person_id, channel, identifier, created_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(channel, identifier) DO UPDATE SET person_id = excluded.person_id
		`, identityID, mePersonID, it.channel, it.identifier, now)
		if err != nil {
			return personsCreated, mePersonID, fmt.Errorf("failed to upsert whoami identity (%s:%s): %w", it.channel, it.identifier, err)
		}
	}

	return personsCreated, mePersonID, nil
}

// syncMessages syncs Eve messages to comms events
func (e *EveAdapter) syncMessages(ctx context.Context, eveDB, commsDB *sql.DB, lastSyncTimestamp int64, contactMap map[int64]string, mePersonID string) (created int, updated int, maxImportedTimestamp int64, perf map[string]string, err error) {
	_ = ctx
	perf = map[string]string{}

	adapterPrefix := e.Name() + ":"
	const contentTypesText = "[\"text\"]"
	const contentTypesAttachment = "[\"attachment\"]"
	const contentTypesTextAttachment = "[\"text\",\"attachment\"]"

	// Query messages from Eve
	query := `
		SELECT
			m.id,
			m.guid,
			m.chat_id,
			m.sender_id,
			m.content,
			CAST(strftime('%s', m.timestamp) AS INTEGER) as timestamp_unix,
			m.is_from_me,
			m.service_name,
			m.reply_to_guid,
			COALESCE(c.chat_identifier, printf('chat_id:%d', m.chat_id)) as thread_id,
			(SELECT COUNT(*) FROM attachments a WHERE a.message_id = m.id) as attachment_count
		FROM messages m
		LEFT JOIN chats c ON m.chat_id = c.id
		WHERE CAST(strftime('%s', m.timestamp) AS INTEGER) > ?
		ORDER BY timestamp_unix ASC
	`

	qStart := time.Now()
	rows, err := eveDB.Query(query, lastSyncTimestamp)
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("failed to query Eve messages: %w", err)
	}
	defer rows.Close()
	perf["query"] = time.Since(qStart).String()

	// Bulk write in a single transaction for SQLite performance.
	txStart := time.Now()
	tx, err := commsDB.Begin()
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("begin comms tx: %w", err)
	}
	defer tx.Rollback()

	stmtInsertEvent, err := tx.Prepare(`
		INSERT OR IGNORE INTO events (
			id, timestamp, channel, content_types, content,
			direction, thread_id, reply_to, source_adapter, source_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("prepare insert event: %w", err)
	}
	defer stmtInsertEvent.Close()

	stmtUpdateEvent, err := tx.Prepare(`
		UPDATE events
		SET
			content = ?,
			content_types = ?,
			thread_id = ?,
			reply_to = ?
		WHERE source_adapter = ?
		  AND source_id = ?
		  AND (
		    content IS NOT ?
		    OR content_types IS NOT ?
		    OR thread_id IS NOT ?
		    OR reply_to IS NOT ?
		  )
	`)
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("prepare update event: %w", err)
	}
	defer stmtUpdateEvent.Close()

	stmtInsertParticipant, err := tx.Prepare(`
		INSERT OR IGNORE INTO event_participants (event_id, person_id, role)
		VALUES (?, ?, ?)
	`)
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("prepare insert participant: %w", err)
	}
	defer stmtInsertParticipant.Close()

	for rows.Next() {
		var messageID int64
		var guid, threadID string
		var chatID, senderID sql.NullInt64
		var content, serviceName, replyToGuid sql.NullString
		var timestamp int64
		var isFromMe bool
		var attachmentCount int

		if err := rows.Scan(&messageID, &guid, &chatID, &senderID, &content, &timestamp, &isFromMe, &serviceName, &replyToGuid, &threadID, &attachmentCount); err != nil {
			return created, updated, maxImportedTimestamp, perf, fmt.Errorf("failed to scan message row: %w", err)
		}

		if timestamp > maxImportedTimestamp {
			maxImportedTimestamp = timestamp
		}

		// Build content types JSON without per-row marshaling.
		contentTypesJSON := contentTypesText
		hasText := content.Valid && content.String != ""
		hasAttachment := attachmentCount > 0
		switch {
		case hasText && hasAttachment:
			contentTypesJSON = contentTypesTextAttachment
		case hasAttachment:
			contentTypesJSON = contentTypesAttachment
		default:
			contentTypesJSON = contentTypesText
		}

		// Determine direction
		direction := "received"
		if isFromMe {
			direction = "sent"
		}

		// Deterministic event ID to avoid UUID cost and extra lookups.
		eventID := adapterPrefix + guid

		res, err := stmtInsertEvent.Exec(
			eventID, timestamp, "imessage", contentTypesJSON, content.String,
			direction, threadID, replyToGuid.String, e.Name(), guid,
		)
		if err != nil {
			return created, updated, maxImportedTimestamp, perf, fmt.Errorf("insert event: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 1 {
			created++
		} else {
			res2, err := stmtUpdateEvent.Exec(
				content.String, contentTypesJSON, threadID, replyToGuid.String,
				e.Name(), guid,
				content.String, contentTypesJSON, threadID, replyToGuid.String,
			)
			if err != nil {
				return created, updated, maxImportedTimestamp, perf, fmt.Errorf("update event: %w", err)
			}
			if n2, _ := res2.RowsAffected(); n2 == 1 {
				updated++
			}
		}

		// Participants: use in-memory contactMap from syncContacts to avoid per-message DB lookups.
		if senderID.Valid {
			if pid, ok := contactMap[senderID.Int64]; ok && pid != "" {
				role := "sender"
				if isFromMe {
					role = "recipient"
				}
				_, _ = stmtInsertParticipant.Exec(eventID, pid, role)
			}
		}
		if isFromMe && mePersonID != "" {
			_, _ = stmtInsertParticipant.Exec(eventID, mePersonID, "sender")
		}
	}

	if err := rows.Err(); err != nil {
		return created, updated, maxImportedTimestamp, perf, err
	}
	if err := tx.Commit(); err != nil {
		return created, updated, maxImportedTimestamp, perf, fmt.Errorf("commit comms tx: %w", err)
	}
	perf["tx_commit"] = time.Since(txStart).String()
	return created, updated, maxImportedTimestamp, perf, nil
}
