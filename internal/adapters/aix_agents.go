package adapters

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// AixAgentsAdapter syncs full fidelity AI session data from AIX to the Agents Ledger.
// This preserves all messages, turns, and tool calls for smart forking and deep analysis.
type AixAgentsAdapter struct {
	source string // cursor, codex, nexus, clawdbot, ...
	dbPath string
}

// NewAixAgentsAdapter creates a new AIX agents adapter for a given source.
func NewAixAgentsAdapter(source string) (*AixAgentsAdapter, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, fmt.Errorf("aix-agents adapter requires source (e.g. cursor)")
	}

	dbPath, err := DefaultAixDBPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("aix database not found at %s (run aix sync --all first): %w", dbPath, err)
	}

	return &AixAgentsAdapter{
		source: source,
		dbPath: dbPath,
	}, nil
}

func (a *AixAgentsAdapter) Name() string {
	return "aix-agents-" + a.source
}

func (a *AixAgentsAdapter) Sync(ctx context.Context, cortexDB *sql.DB, full bool) (SyncResult, error) {
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

	// Sync sessions
	sessionsCreated, sessionsUpdated, maxSessionTS, sessionPerf, err := a.syncSessions(aixDB, cortexDB, lastSync, full)
	if err != nil {
		return result, fmt.Errorf("sync sessions: %w", err)
	}
	result.ThreadsCreated = sessionsCreated
	result.ThreadsUpdated = sessionsUpdated
	for k, v := range sessionPerf {
		result.Perf["sessions_"+k] = v
	}

	// Sync messages
	msgsCreated, msgsUpdated, maxMsgTS, msgPerf, err := a.syncMessages(aixDB, cortexDB, lastSync, full)
	if err != nil {
		return result, fmt.Errorf("sync messages: %w", err)
	}
	result.EventsCreated = msgsCreated
	result.EventsUpdated = msgsUpdated
	for k, v := range msgPerf {
		result.Perf["messages_"+k] = v
	}

	// Sync turns
	turnsCreated, turnsUpdated, maxTurnTS, turnPerf, err := a.syncTurns(aixDB, cortexDB, lastSync, full)
	if err != nil {
		return result, fmt.Errorf("sync turns: %w", err)
	}
	for k, v := range turnPerf {
		result.Perf["turns_"+k] = v
	}
	result.Perf["turns_created"] = fmt.Sprintf("%d", turnsCreated)
	result.Perf["turns_updated"] = fmt.Sprintf("%d", turnsUpdated)

	// Sync tool calls
	toolsCreated, toolsUpdated, maxToolTS, toolPerf, err := a.syncToolCalls(aixDB, cortexDB, lastSync, full)
	if err != nil {
		return result, fmt.Errorf("sync tool calls: %w", err)
	}
	for k, v := range toolPerf {
		result.Perf["tools_"+k] = v
	}
	result.Perf["tools_created"] = fmt.Sprintf("%d", toolsCreated)
	result.Perf["tools_updated"] = fmt.Sprintf("%d", toolsUpdated)

	// Update watermark
	maxTS := maxInt64(maxSessionTS, maxMsgTS, maxTurnTS, maxToolTS)
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

func (a *AixAgentsAdapter) syncSessions(aixDB, cortexDB *sql.DB, lastSync int64, full bool) (created, updated int, maxTS int64, perf map[string]string, err error) {
	perf = map[string]string{}

	query := `
		SELECT
			id, source, project, model, created_at, message_count,
			parent_session_id, parent_message_id, tool_call_id,
			task_description, task_status, is_subagent,
			context_token_limit, context_tokens_used, is_agentic,
			force_mode, workspace_path, context_json, conversation_state,
			raw_json, summary
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
		return 0, 0, 0, perf, fmt.Errorf("query sessions: %w", err)
	}
	defer rows.Close()
	perf["query"] = time.Since(qStart).String()

	txStart := time.Now()
	tx, err := cortexDB.Begin()
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO agent_sessions (
			id, source, project, model, created_at, message_count,
			parent_session_id, parent_message_id, tool_call_id,
			task_description, task_status, is_subagent,
			context_token_limit, context_tokens_used, is_agentic,
			force_mode, workspace_path, context_json, conversation_state,
			raw_json, summary
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			model = excluded.model,
			message_count = excluded.message_count,
			task_status = excluded.task_status,
			context_tokens_used = excluded.context_tokens_used,
			summary = excluded.summary
	`)
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("prepare stmt: %w", err)
	}
	defer stmt.Close()

	for rows.Next() {
		var (
			id, source                                                                       string
			project, model                                                                   sql.NullString
			createdAt, messageCount                                                          sql.NullInt64
			parentSessionID, parentMessageID, toolCallID                                     sql.NullString
			taskDescription, taskStatus                                                      sql.NullString
			isSubagent                                                                       sql.NullInt64
			contextTokenLimit, contextTokensUsed, isAgentic                                  sql.NullInt64
			forceMode, workspacePath, contextJSON, conversationState, rawJSON, summary sql.NullString
		)

		if err := rows.Scan(
			&id, &source, &project, &model, &createdAt, &messageCount,
			&parentSessionID, &parentMessageID, &toolCallID,
			&taskDescription, &taskStatus, &isSubagent,
			&contextTokenLimit, &contextTokensUsed, &isAgentic,
			&forceMode, &workspacePath, &contextJSON, &conversationState,
			&rawJSON, &summary,
		); err != nil {
			return created, updated, maxTS, perf, fmt.Errorf("scan session: %w", err)
		}

		tsSec := int64(0)
		if createdAt.Valid {
			tsSec = createdAt.Int64 / 1000
		}
		if tsSec > maxTS {
			maxTS = tsSec
		}

		res, err := stmt.Exec(
			id, source, nullStr(project), nullStr(model), nullInt(createdAt), nullInt(messageCount),
			nullStr(parentSessionID), nullStr(parentMessageID), nullStr(toolCallID),
			nullStr(taskDescription), nullStr(taskStatus), nullInt(isSubagent),
			nullInt(contextTokenLimit), nullInt(contextTokensUsed), nullInt(isAgentic),
			nullStr(forceMode), nullStr(workspacePath), nullStr(contextJSON), nullStr(conversationState),
			nullStr(rawJSON), nullStr(summary),
		)
		if err != nil {
			return created, updated, maxTS, perf, fmt.Errorf("exec insert session: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			// Check if it was insert or update
			var exists int
			if err := tx.QueryRow("SELECT 1 FROM agent_sessions WHERE id = ? AND created_at IS NOT NULL", id).Scan(&exists); err == nil {
				updated++
			} else {
				created++
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

func (a *AixAgentsAdapter) syncMessages(aixDB, cortexDB *sql.DB, lastSync int64, full bool) (created, updated int, maxTS int64, perf map[string]string, err error) {
	perf = map[string]string{}

	query := `
		SELECT
			m.id, m.session_id, m.role, m.content, m.sequence, m.timestamp,
			m.checkpoint_id, m.is_agentic, m.is_plan_execution, m.context_json, m.cursor_rules_json,
			mm.metadata_json
		FROM messages m
		JOIN sessions s ON m.session_id = s.id
		LEFT JOIN message_metadata mm ON mm.message_id = m.id
		WHERE s.source = ?
	`
	args := []interface{}{a.source}
	if !full {
		query += " AND CAST(COALESCE(m.timestamp, s.created_at) / 1000 AS INTEGER) > ?"
		args = append(args, lastSync)
	}
	query += " ORDER BY m.timestamp ASC, m.id ASC"

	qStart := time.Now()
	rows, err := aixDB.Query(query, args...)
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()
	perf["query"] = time.Since(qStart).String()

	txStart := time.Now()
	tx, err := cortexDB.Begin()
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO agent_messages (
			id, session_id, role, content, sequence, timestamp,
			checkpoint_id, is_agentic, is_plan_execution, context_json, cursor_rules_json,
			metadata_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			content = excluded.content,
			metadata_json = excluded.metadata_json
	`)
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("prepare stmt: %w", err)
	}
	defer stmt.Close()

	for rows.Next() {
		var (
			id, sessionID, role                                               string
			content                                                           sql.NullString
			sequence, timestamp                                               sql.NullInt64
			checkpointID                                                      sql.NullString
			isAgentic, isPlanExecution                                        sql.NullInt64
			contextJSON, cursorRulesJSON, metadataJSON                        sql.NullString
		)

		if err := rows.Scan(
			&id, &sessionID, &role, &content, &sequence, &timestamp,
			&checkpointID, &isAgentic, &isPlanExecution, &contextJSON, &cursorRulesJSON,
			&metadataJSON,
		); err != nil {
			return created, updated, maxTS, perf, fmt.Errorf("scan message: %w", err)
		}

		tsSec := int64(0)
		if timestamp.Valid {
			tsSec = timestamp.Int64 / 1000
		}
		if tsSec > maxTS {
			maxTS = tsSec
		}

		res, err := stmt.Exec(
			id, sessionID, role, nullStr(content), nullInt(sequence), nullInt(timestamp),
			nullStr(checkpointID), nullInt(isAgentic), nullInt(isPlanExecution),
			nullStr(contextJSON), nullStr(cursorRulesJSON), nullStr(metadataJSON),
		)
		if err != nil {
			return created, updated, maxTS, perf, fmt.Errorf("exec insert message: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			created++
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

func (a *AixAgentsAdapter) syncTurns(aixDB, cortexDB *sql.DB, lastSync int64, full bool) (created, updated int, maxTS int64, perf map[string]string, err error) {
	perf = map[string]string{}

	query := `
		SELECT
			t.id, t.session_id, t.parent_turn_id,
			t.query_message_ids, t.response_message_id,
			t.model, t.token_count, t.timestamp,
			t.has_children, t.tool_call_count
		FROM turns t
		JOIN sessions s ON t.session_id = s.id
		WHERE s.source = ?
	`
	args := []interface{}{a.source}
	if !full {
		query += " AND CAST(COALESCE(t.timestamp, 0) / 1000 AS INTEGER) > ?"
		args = append(args, lastSync)
	}
	query += " ORDER BY t.timestamp ASC"

	qStart := time.Now()
	rows, err := aixDB.Query(query, args...)
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("query turns: %w", err)
	}
	defer rows.Close()
	perf["query"] = time.Since(qStart).String()

	txStart := time.Now()
	tx, err := cortexDB.Begin()
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO agent_turns (
			id, session_id, parent_turn_id,
			query_message_ids, response_message_id,
			model, token_count, timestamp,
			has_children, tool_call_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			has_children = excluded.has_children,
			tool_call_count = excluded.tool_call_count
	`)
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("prepare stmt: %w", err)
	}
	defer stmt.Close()

	for rows.Next() {
		var (
			id, sessionID                              string
			parentTurnID, queryMessageIDs, responseMessageID sql.NullString
			model                                      sql.NullString
			tokenCount, timestamp                      sql.NullInt64
			hasChildren, toolCallCount                 sql.NullInt64
		)

		if err := rows.Scan(
			&id, &sessionID, &parentTurnID,
			&queryMessageIDs, &responseMessageID,
			&model, &tokenCount, &timestamp,
			&hasChildren, &toolCallCount,
		); err != nil {
			return created, updated, maxTS, perf, fmt.Errorf("scan turn: %w", err)
		}

		tsSec := int64(0)
		if timestamp.Valid {
			tsSec = timestamp.Int64 / 1000
		}
		if tsSec > maxTS {
			maxTS = tsSec
		}

		res, err := stmt.Exec(
			id, sessionID, nullStr(parentTurnID),
			nullStr(queryMessageIDs), nullStr(responseMessageID),
			nullStr(model), nullInt(tokenCount), nullInt(timestamp),
			nullInt(hasChildren), nullInt(toolCallCount),
		)
		if err != nil {
			return created, updated, maxTS, perf, fmt.Errorf("exec insert turn: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			created++
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

func (a *AixAgentsAdapter) syncToolCalls(aixDB, cortexDB *sql.DB, lastSync int64, full bool) (created, updated int, maxTS int64, perf map[string]string, err error) {
	perf = map[string]string{}

	query := `
		SELECT
			tc.id, tc.message_id, tc.session_id,
			tc.tool_name, tc.tool_number,
			tc.params_json, tc.result_json, tc.status,
			tc.child_session_id, tc.started_at, tc.completed_at
		FROM tool_calls tc
		JOIN sessions s ON tc.session_id = s.id
		WHERE s.source = ?
	`
	args := []interface{}{a.source}
	if !full {
		query += " AND CAST(COALESCE(tc.started_at, tc.completed_at, 0) / 1000 AS INTEGER) > ?"
		args = append(args, lastSync)
	}
	query += " ORDER BY tc.started_at ASC"

	qStart := time.Now()
	rows, err := aixDB.Query(query, args...)
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("query tool_calls: %w", err)
	}
	defer rows.Close()
	perf["query"] = time.Since(qStart).String()

	txStart := time.Now()
	tx, err := cortexDB.Begin()
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO agent_tool_calls (
			id, message_id, session_id,
			tool_name, tool_number,
			params_json, result_json, status,
			child_session_id, started_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			result_json = excluded.result_json,
			status = excluded.status,
			completed_at = excluded.completed_at
	`)
	if err != nil {
		return 0, 0, 0, perf, fmt.Errorf("prepare stmt: %w", err)
	}
	defer stmt.Close()

	for rows.Next() {
		var (
			id, sessionID                              string
			messageID                                  sql.NullString
			toolName                                   sql.NullString
			toolNumber                                 sql.NullInt64
			paramsJSON, resultJSON, status             sql.NullString
			childSessionID                             sql.NullString
			startedAt, completedAt                     sql.NullInt64
		)

		if err := rows.Scan(
			&id, &messageID, &sessionID,
			&toolName, &toolNumber,
			&paramsJSON, &resultJSON, &status,
			&childSessionID, &startedAt, &completedAt,
		); err != nil {
			return created, updated, maxTS, perf, fmt.Errorf("scan tool_call: %w", err)
		}

		tsSec := int64(0)
		if startedAt.Valid {
			tsSec = startedAt.Int64 / 1000
		} else if completedAt.Valid {
			tsSec = completedAt.Int64 / 1000
		}
		if tsSec > maxTS {
			maxTS = tsSec
		}

		res, err := stmt.Exec(
			id, nullStr(messageID), sessionID,
			nullStr(toolName), nullInt(toolNumber),
			nullStr(paramsJSON), nullStr(resultJSON), nullStr(status),
			nullStr(childSessionID), nullInt(startedAt), nullInt(completedAt),
		)
		if err != nil {
			return created, updated, maxTS, perf, fmt.Errorf("exec insert tool_call: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			created++
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

// Helper functions
func nullStr(ns sql.NullString) interface{} {
	if ns.Valid {
		return ns.String
	}
	return nil
}

func nullInt(ni sql.NullInt64) interface{} {
	if ni.Valid {
		return ni.Int64
	}
	return nil
}

func maxInt64(vals ...int64) int64 {
	max := int64(0)
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	return max
}
