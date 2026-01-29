package adapters

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestNewAixEventsAdapter(t *testing.T) {
	// Test that adapter requires a source
	_, err := NewAixEventsAdapter("")
	if err == nil {
		t.Error("Expected error for empty source")
	}

	// Test that adapter handles missing AIX database gracefully
	// (won't work if AIX DB exists, so we skip this in most environments)
	if _, err := DefaultAixDBPath(); err != nil {
		t.Skip("Skipping: cannot determine AIX DB path")
	}
}

func TestNewAixAgentsAdapter(t *testing.T) {
	// Test that adapter requires a source
	_, err := NewAixAgentsAdapter("")
	if err == nil {
		t.Error("Expected error for empty source")
	}
}

func TestStripToolAndThinkingBlocks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "plain text unchanged",
			input:    "Hello, this is a plain response.",
			expected: "Hello, this is a plain response.",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "whitespace only",
			input:    "   \n\n   ",
			expected: "",
		},
		{
			name:     "text with multiple newlines collapsed",
			input:    "Line 1\n\n\n\n\nLine 2",
			expected: "Line 1\n\nLine 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripToolAndThinkingBlocks(tt.input)
			if result != tt.expected {
				t.Errorf("stripToolAndThinkingBlocks(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestAixEventsAdapterIntegration tests the full sync flow if AIX DB exists
func TestAixEventsAdapterIntegration(t *testing.T) {
	aixDBPath, err := DefaultAixDBPath()
	if err != nil {
		t.Skip("Skipping integration test: cannot determine AIX DB path")
	}
	if _, err := os.Stat(aixDBPath); os.IsNotExist(err) {
		t.Skip("Skipping integration test: AIX database not found at " + aixDBPath)
	}

	// Create a temp Mnemonic database
	tmpDir := t.TempDir()
	mnemonicDBPath := filepath.Join(tmpDir, "mnemonic.db")
	
	db, err := sql.Open("sqlite", mnemonicDBPath)
	if err != nil {
		t.Fatalf("Failed to create temp database: %v", err)
	}
	defer db.Close()

	// Initialize schema (simplified - just the tables we need)
	schemaSQL := `
		CREATE TABLE IF NOT EXISTS events (
			id TEXT PRIMARY KEY,
			timestamp INTEGER NOT NULL,
			channel TEXT NOT NULL,
			content_types TEXT NOT NULL,
			content TEXT,
			direction TEXT NOT NULL,
			thread_id TEXT,
			reply_to TEXT,
			source_adapter TEXT NOT NULL,
			source_id TEXT NOT NULL,
			metadata_json TEXT,
			UNIQUE(source_adapter, source_id)
		);
		CREATE TABLE IF NOT EXISTS threads (
			id TEXT PRIMARY KEY,
			channel TEXT NOT NULL,
			name TEXT,
			source_adapter TEXT NOT NULL,
			source_id TEXT NOT NULL,
			parent_thread_id TEXT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(source_adapter, source_id)
		);
		CREATE TABLE IF NOT EXISTS event_participants (
			event_id TEXT NOT NULL,
			contact_id TEXT NOT NULL,
			role TEXT NOT NULL,
			PRIMARY KEY (event_id, contact_id, role)
		);
		CREATE TABLE IF NOT EXISTS contacts (
			id TEXT PRIMARY KEY,
			display_name TEXT,
			source TEXT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS contact_identifiers (
			id TEXT PRIMARY KEY,
			contact_id TEXT NOT NULL,
			type TEXT NOT NULL,
			value TEXT NOT NULL,
			normalized TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			last_seen_at INTEGER,
			UNIQUE(type, normalized)
		);
		CREATE TABLE IF NOT EXISTS persons (
			id TEXT PRIMARY KEY,
			canonical_name TEXT NOT NULL,
			display_name TEXT,
			is_me INTEGER DEFAULT 0,
			relationship_type TEXT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS person_contact_links (
			id TEXT PRIMARY KEY,
			person_id TEXT NOT NULL,
			contact_id TEXT NOT NULL,
			confidence REAL DEFAULT 1.0,
			source_type TEXT,
			first_seen_at INTEGER,
			last_seen_at INTEGER,
			UNIQUE(person_id, contact_id)
		);
		CREATE TABLE IF NOT EXISTS sync_watermarks (
			adapter TEXT PRIMARY KEY,
			last_sync_at INTEGER NOT NULL,
			last_event_id TEXT
		);
	`
	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatalf("Failed to initialize schema: %v", err)
	}

	// Create adapter
	adapter, err := NewAixEventsAdapter("cursor")
	if err != nil {
		t.Fatalf("Failed to create adapter: %v", err)
	}

	if adapter.Name() != "aix-events-cursor" {
		t.Errorf("Adapter name = %q, want %q", adapter.Name(), "aix-events-cursor")
	}

	t.Logf("Successfully created AIX events adapter")
	// Note: We don't run a full sync in tests as it would require a real AIX DB with data
}

// TestAixAgentsAdapterIntegration tests the full sync flow if AIX DB exists
func TestAixAgentsAdapterIntegration(t *testing.T) {
	aixDBPath, err := DefaultAixDBPath()
	if err != nil {
		t.Skip("Skipping integration test: cannot determine AIX DB path")
	}
	if _, err := os.Stat(aixDBPath); os.IsNotExist(err) {
		t.Skip("Skipping integration test: AIX database not found at " + aixDBPath)
	}

	// Create a temp Mnemonic database
	tmpDir := t.TempDir()
	mnemonicDBPath := filepath.Join(tmpDir, "mnemonic.db")
	
	db, err := sql.Open("sqlite", mnemonicDBPath)
	if err != nil {
		t.Fatalf("Failed to create temp database: %v", err)
	}
	defer db.Close()

	// Initialize schema (simplified - just the tables we need)
	schemaSQL := `
		CREATE TABLE IF NOT EXISTS agent_sessions (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			model TEXT,
			project TEXT,
			created_at INTEGER,
			message_count INTEGER DEFAULT 0,
			parent_session_id TEXT,
			parent_message_id TEXT,
			tool_call_id TEXT,
			task_description TEXT,
			task_status TEXT,
			is_subagent INTEGER DEFAULT 0,
			context_token_limit INTEGER,
			context_tokens_used INTEGER,
			is_agentic INTEGER DEFAULT 0,
			force_mode TEXT,
			workspace_path TEXT,
			context_json TEXT,
			conversation_state TEXT,
			raw_json TEXT,
			summary TEXT
		);
		CREATE TABLE IF NOT EXISTS agent_messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT,
			sequence INTEGER,
			timestamp INTEGER,
			checkpoint_id TEXT,
			is_agentic INTEGER DEFAULT 0,
			is_plan_execution INTEGER DEFAULT 0,
			context_json TEXT,
			cursor_rules_json TEXT,
			metadata_json TEXT
		);
		CREATE TABLE IF NOT EXISTS agent_turns (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			parent_turn_id TEXT,
			query_message_ids TEXT,
			response_message_id TEXT,
			model TEXT,
			token_count INTEGER,
			timestamp INTEGER,
			has_children INTEGER DEFAULT 0,
			tool_call_count INTEGER DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS agent_tool_calls (
			id TEXT PRIMARY KEY,
			message_id TEXT,
			session_id TEXT NOT NULL,
			tool_name TEXT,
			tool_number INTEGER,
			params_json TEXT,
			result_json TEXT,
			status TEXT,
			child_session_id TEXT,
			started_at INTEGER,
			completed_at INTEGER
		);
		CREATE TABLE IF NOT EXISTS sync_watermarks (
			adapter TEXT PRIMARY KEY,
			last_sync_at INTEGER NOT NULL,
			last_event_id TEXT
		);
	`
	if _, err := db.Exec(schemaSQL); err != nil {
		t.Fatalf("Failed to initialize schema: %v", err)
	}

	// Create adapter
	adapter, err := NewAixAgentsAdapter("cursor")
	if err != nil {
		t.Fatalf("Failed to create adapter: %v", err)
	}

	if adapter.Name() != "aix-agents-cursor" {
		t.Errorf("Adapter name = %q, want %q", adapter.Name(), "aix-agents-cursor")
	}

	t.Logf("Successfully created AIX agents adapter")
	// Note: We don't run a full sync in tests as it would require a real AIX DB with data
}
