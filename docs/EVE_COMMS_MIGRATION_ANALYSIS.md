# Eve → Comms Migration Analysis

## Assignment

Analyze the current Eve and Comms architectures and design a migration path where:
1. Eve becomes a "dumb" adapter that only ETLs iMessage data into comms.db
2. All analysis capabilities (conversation chunking, embeddings, entity extraction, topic analysis, etc.) move UP into Comms
3. The analysis layer becomes generic and applies to ALL communication channels (iMessage, Gmail, AIX, Discord, etc.)
4. eve.db eventually goes away - Eve just writes directly to comms.db

## Context

### Current Architecture

**Eve** (`~/nexus/home/projects/eve/`)
- Reads from macOS chat.db (iMessage)
- Maintains its own eve.db warehouse
- Has sophisticated analysis:
  - Conversation chunking (90-minute sliding window)
  - Gemini embeddings for conversations, entities, topics, emotions
  - Entity extraction, topic extraction, emotion analysis
  - Task engine for background processing
  - Semantic search

**Comms** (`~/nexus/home/projects/comms/`)
- Root orchestrator for ALL communication channels
- Has adapters for: Eve (iMessage), Gmail, AIX, Google Contacts, Calendar
- Maintains unified comms.db with events, persons, identities
- Identity resolution across channels
- Currently fairly thin - adapters do most work

### The Problem

Analysis capabilities are stuck in Eve, but they're useful for ALL channels:
- Conversation chunking works for email threads too
- Entity/topic extraction works on any text
- Embeddings work on any conversation
- The task engine is generic

### Desired End State

```
┌─────────────────────────────────────────────────────────────────┐
│                         COMMS (Smart)                           │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ADAPTERS (Dumb - just format conversion)                      │
│  ├── eve adapter    → chat.db → comms events                   │
│  ├── gmail adapter  → Gmail API → comms events                 │
│  ├── aix adapter    → cursor.db → comms events                 │
│  └── future...      → any source → comms events                │
│                                                                 │
│  CONVERSATION CHUNKING (channel-agnostic)                      │
│  ├── Time-based chunking (90 min window, daily, etc.)         │
│  ├── Thread-based chunking (email threads)                     │
│  └── Session-based chunking (AI sessions)                      │
│                                                                 │
│  ANALYSIS ENGINE (from eve)                                    │
│  ├── PII Extraction (new)                                      │
│  ├── Entity extraction                                         │
│  ├── Topic extraction                                          │
│  ├── Emotion analysis                                          │
│  ├── Embeddings (Gemini)                                       │
│  └── Semantic search                                           │
│                                                                 │
│  TASK ENGINE (from eve)                                        │
│  ├── Background job processing                                 │
│  ├── Queue management                                          │
│  └── Rate limiting for API calls                               │
│                                                                 │
│  IDENTITY RESOLUTION (new)                                     │
│  ├── PII-based identity linking                               │
│  └── Cross-channel merging                                     │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## Key Files to Analyze

### Eve - Analysis & Chunking
- `internal/etl/conversations.go` - Conversation chunking logic (90 min window)
- `internal/engine/embeddings_job.go` - Embedding generation
- `internal/engine/analysis_job.go` - LLM analysis jobs
- `internal/queue/` - Job queue system
- `internal/gemini/` - Gemini API client
- `skills/eve/prompts/analysis/` - Analysis prompts

### Eve - ETL & Sync
- `internal/etl/` - chat.db ETL pipeline
- `internal/db/` - Database operations
- `cmd/eve/main.go` - CLI commands

### Comms - Current State
- `internal/adapters/` - Current adapters (eve, gmail, contacts, etc.)
- `internal/db/schema.sql` - Database schema
- `internal/identify/` - Identity resolution
- `internal/sync/` - Sync orchestration

### Task Engine
- `~/nexus/home/projects/taskengine/` - Shared task engine (already extracted?)

## Analysis Questions

1. **Conversation Chunking**
   - How does Eve's 90-min window chunking work?
   - How would this apply to email (thread-based)?
   - How would this apply to AI sessions (already session-based)?
   - What's the abstraction layer look like?

2. **Analysis Pipeline**
   - What prompts does Eve use for entity/topic/emotion extraction?
   - How are these stored in eve.db?
   - What schema changes needed in comms.db?
   - How to make these generic for any channel?

3. **Embeddings**
   - What's embedded currently? (conversations, entities, topics, emotions)
   - How is semantic search implemented?
   - What would the comms-level embedding strategy be?

4. **Task Engine**
   - Is taskengine already a separate project?
   - What's the queue storage mechanism?
   - How to integrate with comms?

5. **Migration Path**
   - Can we do this incrementally?
   - What's the order of operations?
   - How to handle eve.db data during migration?

## Expected Deliverables

1. **Architecture Document**
   - Detailed design for comms analysis layer
   - Schema changes for comms.db
   - API design for analysis engine

2. **Migration Plan**
   - Step-by-step migration path
   - Data migration strategy for eve.db → comms.db
   - Backwards compatibility considerations

3. **Implementation Tickets**
   - Break down into implementable chunks
   - Dependencies between pieces
   - Estimated complexity

## Related Documents

- `/Users/tyler/nexus/home/projects/comms/docs/IDENTITY_RESOLUTION_PLAN.md` - Identity resolution plan
- `/Users/tyler/nexus/home/projects/comms/prompts/pii-extraction-v1.prompt.md` - PII extraction prompt
- `/Users/tyler/nexus/home/projects/eve/skills/eve/prompts/analysis/` - Eve analysis prompts

## Notes

- The PII extraction piece is being developed separately and will integrate with this
- The goal is to have ONE place (comms) where all communication analysis happens
- Eve becomes just an ETL adapter for iMessage, nothing more
- This enables consistent analysis across all channels
