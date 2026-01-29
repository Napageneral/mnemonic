# Mnemonic Architecture Specification

**Status:** DESIGN SPEC  
**Last Updated:** 2026-01-29  
**Previous Name:** Cortex  

---

## Executive Summary

**Mnemonic** is the unified memory system for Nexus. It consolidates data from multiple sources into a searchable, analyzable memory graph with support for extraction, embeddings, and semantic search.

**Key Insight:** Different data domains (events, AI sessions, documents) share the same pipeline pattern:
1. **Ingest** → pull data from sources into normalized schema
2. **Chunk** → group into meaningful episodes
3. **Analyze** → extract facets, generate embeddings, run LLM analysis
4. **Search** → semantic search, filtering, retrieval

The architecture separates **domain-specific tables** (ledgers) from **shared infrastructure** (core ledger), allowing each domain to have its own schema while sharing the heavy machinery.

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                         Mnemonic                                │
│                   (Unified Memory System)                       │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌───────────────────────────────────────────────────────────┐ │
│  │                      Core Ledger                          │ │
│  │  episodes, episode_definitions, episode_events            │ │
│  │  analysis_types, analysis_runs, facets                    │ │
│  │  embeddings, embedding_configs                            │ │
│  │  (shared infrastructure for all domains)                  │ │
│  └───────────────────────────────────────────────────────────┘ │
│                              ▲                                  │
│              ┌───────────────┴───────────────┐                 │
│              │                               │                  │
│  ┌───────────┴───────────┐     ┌────────────┴────────────┐    │
│  │     Events Ledger     │     │      Agents Ledger      │    │
│  │                       │     │                         │    │
│  │  events               │     │  agent_sessions         │    │
│  │  threads              │     │  agent_messages         │    │
│  │                       │     │  agent_turns            │    │
│  │  (trimmed AI turns    │     │  agent_tool_calls       │    │
│  │   + iMessage, Gmail,  │     │                         │    │
│  │   calendar, etc.)     │     │  (full fidelity AI data │    │
│  │                       │     │   for smart forking)    │    │
│  └───────────────────────┘     └─────────────────────────┘    │
│              ▲                               ▲                  │
└──────────────┼───────────────────────────────┼──────────────────┘
               │                               │
        ┌──────┴──────┐                 ┌──────┴──────┐
        │   Multiple  │                 │ AIX Adapter │
        │   Adapters  │                 │   (full)    │
        └──────┬──────┘                 └──────┬──────┘
               │                               │
    ┌──────────┼──────────┐                   │
    │          │          │                   │
 iMessage   Gmail     Calendar           ┌────┴────┐
                          │              │   AIX   │
                    AIX Adapter          │(capture)│
                     (trimmed)           └────┬────┘
                          │                   │
                          └─────────┬─────────┘
                                    │
                         ┌──────────┴──────────┐
                         │ Cursor/Codex/Nexus  │
                         └─────────────────────┘
```

---

## Ledger Definitions

### Core Ledger (Shared Infrastructure)

The core ledger provides the shared machinery for all domains:

```sql
-- Episode system: chunks data into meaningful units
CREATE TABLE episode_definitions (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,              -- "aix_turn", "time_gap_90min", etc.
    channel TEXT,                           -- NULL = all channels
    strategy TEXT NOT NULL,                 -- "turn", "time_gap", "single_event"
    config_json TEXT NOT NULL,
    description TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE episodes (
    id TEXT PRIMARY KEY,
    definition_id TEXT NOT NULL REFERENCES episode_definitions(id),
    channel TEXT,
    thread_id TEXT,                         -- Links to events.threads or agents.agent_sessions
    start_time INTEGER NOT NULL,
    end_time INTEGER NOT NULL,
    event_count INTEGER NOT NULL,
    first_event_id TEXT,
    last_event_id TEXT,
    created_at INTEGER NOT NULL
);

-- Junction table: links episodes to their source records
CREATE TABLE episode_events (
    episode_id TEXT REFERENCES episodes(id),
    event_id TEXT,                          -- Could be events.id or agent_messages.id
    position INTEGER,
    PRIMARY KEY (episode_id, event_id)
);

-- Analysis system: runs extraction/analysis on episodes
CREATE TABLE analysis_types (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    version TEXT NOT NULL,
    output_type TEXT NOT NULL,              -- "structured", "freeform"
    facets_config_json TEXT,
    prompt_template TEXT,
    model TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE analysis_runs (
    id TEXT PRIMARY KEY,
    analysis_type_id TEXT NOT NULL REFERENCES analysis_types(id),
    episode_id TEXT NOT NULL REFERENCES episodes(id),
    status TEXT NOT NULL,                   -- "pending", "running", "completed", "failed"
    started_at INTEGER,
    completed_at INTEGER,
    output_text TEXT,
    error_message TEXT,
    created_at INTEGER NOT NULL,
    UNIQUE(analysis_type_id, episode_id)
);

CREATE TABLE facets (
    id TEXT PRIMARY KEY,
    analysis_run_id TEXT NOT NULL REFERENCES analysis_runs(id),
    episode_id TEXT NOT NULL REFERENCES episodes(id),
    facet_type TEXT NOT NULL,               -- "entity", "topic", "file_reference", etc.
    value TEXT NOT NULL,
    person_id TEXT,
    confidence REAL,
    metadata_json TEXT,
    created_at INTEGER NOT NULL
);

-- Embeddings system
CREATE TABLE embedding_configs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    model TEXT NOT NULL,
    dimensions INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE embeddings (
    id TEXT PRIMARY KEY,
    config_id TEXT NOT NULL REFERENCES embedding_configs(id),
    episode_id TEXT REFERENCES episodes(id),
    event_id TEXT,                          -- For event-level embeddings
    vector BLOB NOT NULL,
    created_at INTEGER NOT NULL
);
```

### Events Ledger

For human-relevant events: messages, emails, calendar, and **trimmed AI turns**.

```sql
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    timestamp INTEGER NOT NULL,
    channel TEXT NOT NULL,                  -- "imessage", "gmail", "cursor", "calendar"
    content_types TEXT NOT NULL,            -- JSON: ["text"], ["text", "image"]
    content TEXT,
    direction TEXT NOT NULL,                -- "sent", "received", "observed"
    thread_id TEXT REFERENCES threads(id),
    reply_to TEXT,
    source_adapter TEXT NOT NULL,
    source_id TEXT NOT NULL,
    metadata_json TEXT,
    UNIQUE(source_adapter, source_id)
);

CREATE TABLE threads (
    id TEXT PRIMARY KEY,
    channel TEXT NOT NULL,
    name TEXT,
    source_adapter TEXT NOT NULL,
    source_id TEXT NOT NULL,
    parent_thread_id TEXT REFERENCES threads(id),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(source_adapter, source_id)
);

CREATE INDEX idx_events_timestamp ON events(timestamp);
CREATE INDEX idx_events_channel ON events(channel);
CREATE INDEX idx_events_thread ON events(thread_id);
```

**What goes into Events:**
- iMessage conversations
- Gmail emails
- Calendar events
- **Trimmed AI turns** — user message + consolidated assistant response (1 event each)

**What does NOT go into Events:**
- Individual tool calls
- Assistant thinking traces
- Intermediate assistant bubbles
- Full session metadata

### Agents Ledger

Full fidelity AI session data for smart forking, analysis, and replay.

```sql
CREATE TABLE agent_sessions (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,                   -- "cursor", "codex", "nexus", "clawdbot"
    model TEXT,
    project TEXT,
    created_at INTEGER,
    message_count INTEGER,
    
    -- Subagent linking
    parent_session_id TEXT REFERENCES agent_sessions(id),
    parent_message_id TEXT,
    tool_call_id TEXT,
    task_description TEXT,
    task_status TEXT,
    is_subagent INTEGER DEFAULT 0,
    
    -- Session context
    context_token_limit INTEGER,
    context_tokens_used INTEGER,
    is_agentic INTEGER DEFAULT 0,
    force_mode TEXT,
    workspace_path TEXT,
    context_json TEXT,
    conversation_state TEXT,
    
    -- Raw data
    raw_json TEXT
);

CREATE TABLE agent_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES agent_sessions(id),
    role TEXT NOT NULL,                     -- "user", "assistant", "system", "tool"
    content TEXT,
    sequence INTEGER,
    timestamp INTEGER,
    
    -- Message metadata
    checkpoint_id TEXT,
    is_agentic INTEGER DEFAULT 0,
    is_plan_execution INTEGER DEFAULT 0,
    context_json TEXT,
    cursor_rules_json TEXT,
    metadata_json TEXT                      -- Full original metadata
);

CREATE TABLE agent_turns (
    id TEXT PRIMARY KEY,                    -- Same as final response message ID
    session_id TEXT NOT NULL REFERENCES agent_sessions(id),
    parent_turn_id TEXT REFERENCES agent_turns(id),
    
    query_message_ids TEXT,                 -- JSON array
    response_message_id TEXT REFERENCES agent_messages(id),
    
    model TEXT,
    token_count INTEGER,
    timestamp INTEGER,
    has_children INTEGER DEFAULT 0,
    tool_call_count INTEGER DEFAULT 0
);

CREATE TABLE agent_tool_calls (
    id TEXT PRIMARY KEY,
    message_id TEXT REFERENCES agent_messages(id),
    session_id TEXT NOT NULL REFERENCES agent_sessions(id),
    
    tool_name TEXT,
    tool_number INTEGER,
    params_json TEXT,
    result_json TEXT,
    status TEXT,
    
    child_session_id TEXT REFERENCES agent_sessions(id),
    started_at INTEGER,
    completed_at INTEGER
);

-- Indexes
CREATE INDEX idx_agent_sessions_source ON agent_sessions(source);
CREATE INDEX idx_agent_sessions_parent ON agent_sessions(parent_session_id);
CREATE INDEX idx_agent_messages_session ON agent_messages(session_id);
CREATE INDEX idx_agent_turns_session ON agent_turns(session_id);
CREATE INDEX idx_agent_tool_calls_session ON agent_tool_calls(session_id);
CREATE INDEX idx_agent_tool_calls_child ON agent_tool_calls(child_session_id);
```

---

## AIX Integration

### AIX's Role: Capture Only

AIX is the capture layer for AI sessions. It syncs from Cursor, Codex, and future harnesses into a local SQLite database.

**AIX does:**
- Parse Cursor's `state.vscdb` database
- Parse Codex/Claude Code JSONL sessions
- Parse Nexus pi-coding-agent sessions
- Store full fidelity data locally
- Export to Mnemonic

**AIX does NOT:**
- Run analysis or extraction
- Generate embeddings
- Perform smart forking
- Handle semantic search

### Two AIX Adapters in Mnemonic

#### 1. `aix-events` Adapter

Exports **trimmed turns** to the Events ledger.

```go
// For each AIX turn:
// 1. Consolidate user messages into ONE event
// 2. Extract assistant's final response (drop tool calls, thinking) into ONE event
// 3. Create thread for the session

func (a *AIXEventsAdapter) SyncTrimmedTurns(ctx context.Context) error {
    turns := a.aix.GetAllTurns()
    
    for _, turn := range turns {
        // User message event
        userContent := consolidateUserMessages(turn.QueryMessageIDs)
        userEvent := Event{
            ID:            fmt.Sprintf("aix_turn_user:%s", turn.ID),
            Timestamp:     turn.Timestamp,
            Channel:       "cursor",  // or codex, nexus, etc.
            Content:       userContent,
            Direction:     "sent",
            ThreadID:      fmt.Sprintf("aix_session:%s", turn.SessionID),
            SourceAdapter: "aix-events",
            SourceID:      turn.ID + ":user",
        }
        
        // Assistant response event (trimmed: just the final text)
        assistantContent := extractFinalResponse(turn.ResponseMessageID)
        assistantEvent := Event{
            ID:            fmt.Sprintf("aix_turn_assistant:%s", turn.ID),
            Timestamp:     turn.Timestamp,
            Channel:       "cursor",
            Content:       assistantContent,
            Direction:     "received",
            ThreadID:      fmt.Sprintf("aix_session:%s", turn.SessionID),
            SourceAdapter: "aix-events",
            SourceID:      turn.ID + ":assistant",
        }
        
        a.db.UpsertEvent(userEvent)
        a.db.UpsertEvent(assistantEvent)
    }
}
```

**Result:** ~18k user events + ~18k assistant events (from ~18k turns), not 294k+14k raw messages.

#### 2. `aix-agents` Adapter

Exports **full fidelity** to the Agents ledger.

```go
func (a *AIXAgentsAdapter) SyncFullFidelity(ctx context.Context) error {
    sessions := a.aix.GetAllSessions()
    
    for _, session := range sessions {
        // Copy session with all metadata
        agentSession := AgentSession{
            ID:                 session.ID,
            Source:             session.Source,
            Model:              session.Model,
            ParentSessionID:    session.ParentSessionID,
            ToolCallID:         session.ToolCallID,
            IsSubagent:         session.IsSubagent,
            ContextTokenLimit:  session.ContextTokenLimit,
            ContextTokensUsed:  session.ContextTokensUsed,
            // ... all fields
        }
        a.db.UpsertAgentSession(agentSession)
        
        // Copy all messages with full metadata
        for _, msg := range session.Messages {
            agentMsg := AgentMessage{
                ID:           msg.ID,
                SessionID:    session.ID,
                Role:         msg.Role,
                Content:      msg.Content,
                Sequence:     msg.Sequence,
                MetadataJSON: msg.MetadataJSON,
                // ... all fields
            }
            a.db.UpsertAgentMessage(agentMsg)
        }
        
        // Copy turns
        for _, turn := range session.Turns {
            a.db.UpsertAgentTurn(turn)
        }
        
        // Copy tool calls
        for _, tc := range session.ToolCalls {
            a.db.UpsertAgentToolCall(tc)
        }
    }
}
```

---

## Episode Strategies

### For Events Ledger

| Strategy | Config | Use Case |
|----------|--------|----------|
| `time_gap` | `{gap_seconds: 5400, scope: "thread"}` | iMessage conversations (90min gap) |
| `thread` | `{}` | Gmail threads (one episode per thread) |
| `single_event` | `{}` | Calendar events |
| `aix_turn` | `{}` | AI conversation turns (user + assistant pair) |

### For Agents Ledger

| Strategy | Config | Use Case |
|----------|--------|----------|
| `agent_turn` | `{}` | One episode per agent_turn |
| `agent_session` | `{}` | One episode per session (for session-level analysis) |

---

## Smart Forking

Smart forking lives **in Mnemonic**, not AIX. It requires:

1. **Embedding search** — find relevant past turns
2. **Episode analysis** — understand what happened in past conversations
3. **Facet extraction** — identify entities, topics, decisions
4. **Context assembly** — build optimal context for new session

### Fork Context Builder

```go
type ForkContextBuilder struct {
    agents *AgentsLedger
    search *SearchEngine
}

func (b *ForkContextBuilder) BuildForkContext(query string, opts ForkOptions) (*ForkContext, error) {
    // 1. Semantic search over agent_turns
    relevantTurns := b.search.SearchAgentTurns(query, opts.MaxTurns)
    
    // 2. Get full context for each turn
    contexts := make([]TurnContext, 0)
    for _, turn := range relevantTurns {
        session := b.agents.GetSession(turn.SessionID)
        messages := b.agents.GetMessagesForTurn(turn)
        toolCalls := b.agents.GetToolCallsForTurn(turn)
        
        contexts = append(contexts, TurnContext{
            Turn:      turn,
            Session:   session,
            Messages:  messages,
            ToolCalls: toolCalls,
        })
    }
    
    // 3. Build fork context
    return &ForkContext{
        RelevantTurns: contexts,
        TotalTokens:   calculateTokens(contexts),
        Query:         query,
    }, nil
}
```

---

## Migration Path (Cortex → Mnemonic)

### Phase 1: Rename and Restructure

1. Rename package/binary: `cortex` → `mnemonic`
2. Split schema into Core + Events + Agents ledgers
3. Prefix Agents tables with `agent_` to avoid conflicts

### Phase 2: AIX Adapters

1. Implement `aix-events` adapter for trimmed turns
2. Implement `aix-agents` adapter for full fidelity
3. Test both adapters with existing AIX data

### Phase 3: Episode Strategies

1. Add `aix_turn` episode definition for Events
2. Add `agent_turn` and `agent_session` definitions for Agents
3. Wire up chunking for both ledgers

### Phase 4: Analysis & Embeddings

1. Ensure analysis runs work on both ledgers
2. Generate embeddings for agent_turns (for smart forking)
3. Implement ForkContextBuilder

### Phase 5: CLI Updates

```bash
# Sync all sources
mnemonic sync

# Sync specific adapters
mnemonic sync --adapter imessage
mnemonic sync --adapter gmail
mnemonic sync --adapter aix-events
mnemonic sync --adapter aix-agents

# Run analysis
mnemonic analyze --type memory_extraction
mnemonic analyze --type entity_extraction

# Search
mnemonic search "authentication flow"
mnemonic search --ledger agents "subagent dispatch"

# Smart forking (future)
mnemonic fork --query "user authentication" --max-turns 10
```

---

## Key Design Decisions

### 1. Why Two AIX Adapters?

The trimmed vs full split serves different purposes:
- **Events ledger** = what the user saw and read (memory extraction)
- **Agents ledger** = full computation log (smart forking, replay, analysis)

Mixing them would either bloat Events with tool call noise or lose fidelity in Agents.

### 2. Why Not a Documents Ledger?

Not designing for it now. Will add when needed. The architecture supports it — just another ledger with its own tables and chunking strategy.

### 3. Why Smart Forking in Mnemonic?

Smart forking requires:
- Embedding search (Mnemonic has this)
- Episode analysis (Mnemonic has this)
- Facet extraction (Mnemonic has this)

AIX is capture-only. Building fork infrastructure there would duplicate all of Mnemonic's machinery.

### 4. Why Prefix Agents Tables?

Using `agent_sessions`, `agent_messages`, etc. instead of `sessions`, `messages` to:
- Clearly distinguish from Events ledger tables
- Allow both ledgers in the same database
- Enable future cross-ledger queries without confusion

---

## References

- `CORTEX_IMPLEMENTATION_SPEC.md` — Previous architecture (pre-unification)
- `aix/docs/AIX_FULL_INGESTION_SPEC.md` — AIX schema and parsing
- `aix/docs/AIX_MNEMONIC_PIPELINE.md` — How AIX feeds into Mnemonic
- `nexus-specs/specs/agent-system/ONTOLOGY.md` — Nexus ontology alignment
- `nexus-specs/specs/agent-system/SESSION_FORMAT.md` — Session format for native Nexus sessions

---

*This document is the canonical reference for Mnemonic architecture. Cortex is now Mnemonic.*
