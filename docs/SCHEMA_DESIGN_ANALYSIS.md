# Schema Design Analysis: Eve → Comms Migration

## 1. Schema Comparison & Design Decisions

### Eve Schema (Reference)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           EVE.DB SCHEMA                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  CORE DATA (ETL from chat.db)                                               │
│  ├── contacts (id, name, nickname, avatar, is_me)                          │
│  ├── contact_identifiers (contact_id, identifier, type, is_primary)        │
│  ├── chats (id, chat_identifier, chat_name, is_group, service_name,        │
│  │          total_messages, last_embedding_update, wrapped_*)               │
│  ├── chat_participants (chat_id, contact_id)                               │
│  ├── messages (id, guid, chat_id, sender_id, content, timestamp,           │
│  │             is_from_me, message_type, service_name,                     │
│  │             associated_message_guid, reply_to_guid, conversation_id)    │
│  ├── reactions (id, guid, original_message_guid, sender_id, chat_id,       │
│  │              reaction_type, is_from_me, timestamp)                      │
│  └── attachments (id, guid, message_id, file_name, mime_type, size,        │
│                   is_sticker, uti, created_date)                           │
│                                                                             │
│  CONVERSATION CHUNKING                                                      │
│  └── conversations (id, chat_id, initiator_id, start_time, end_time,       │
│                     message_count, summary, gap_threshold)                 │
│                                                                             │
│  ANALYSIS INFRASTRUCTURE                                                    │
│  ├── prompt_templates (id, name, description, template_text)               │
│  ├── completions (id, conversation_id, chat_id, contact_id,                │
│  │                prompt_template_id, compiled_prompt_text, model, result) │
│  └── conversation_analyses (id, conversation_id, prompt_template_id,       │
│                             eve_prompt_id, status, completion_id,          │
│                             blocked_reason, blocked_reason_message)        │
│                                                                             │
│  ANALYSIS OUTPUTS (convo-all-v1)                                           │
│  ├── entities (conversation_id, chat_id, contact_id, title)                │
│  ├── topics (conversation_id, chat_id, contact_id, title)                  │
│  ├── emotions (conversation_id, chat_id, contact_id, emotion_type)         │
│  └── humor_items (conversation_id, chat_id, contact_id, snippet)           │
│                                                                             │
│  EMBEDDINGS                                                                 │
│  └── embeddings (entity_type, entity_id, model, embedding_blob, dimension) │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Design Decisions

| Eve Concept | Comms Design | Notes |
|-------------|--------------|-------|
| **contacts** | persons | ✅ Already equivalent |
| **contact_identifiers** | identities | ✅ Already equivalent (comms adds `channel`) |
| **chats** | **threads** (new) | Generic concept: email subjects, discord channels, cursor sessions, iMessage chats |
| **chat_participants** | event_participants | ✅ Better approach - membership captured per-event, handles dynamic membership |
| **is_group** | ❌ Skip | Reconstructable from event_participants. 1:1 can become group (email CC). |
| **messages** | events | ✅ Already equivalent |
| **reactions** | events + content_type | Reactions are events with `content_type: "reaction"`, `reply_to` refs the reacted event |
| **attachments** | **attachments** (new) | Dedicated table with metadata (mime_type, filename, storage_uri, etc.) |
| **conversations** | **conversations** (new) | Flexible model with definitions |
| **conversation_analyses** | **analysis_runs** (new) | Generic analysis framework |
| **entities/topics/etc** | **facets** (new) | Unified queryable extraction table |
| **embeddings** | **embeddings** (new) | Same pattern as Eve |
| **completions** | ❌ Skip (optional) | Raw LLM output - only keep if needed for debugging |

---

## 2. Threads Table (Chat/Channel Metadata)

Threads capture the generic concept of a "container" for events:
- **iMessage**: A chat (1:1 or group)
- **Gmail**: An email thread (subject line)
- **Discord**: A channel or thread within a channel
- **Slack**: A channel or thread
- **Cursor/AIX**: A session

```sql
-- Threads: grouping containers for events
CREATE TABLE threads (
    id TEXT PRIMARY KEY,
    channel TEXT NOT NULL,                 -- "imessage", "gmail", "discord", "slack", "aix"
    
    -- Display info
    name TEXT,                             -- Chat name, email subject, channel name, session title
    
    -- Source tracking
    source_adapter TEXT NOT NULL,
    source_id TEXT NOT NULL,               -- Original ID from source system
    
    -- Parent thread (for Slack/Discord thread-in-channel)
    parent_thread_id TEXT REFERENCES threads(id),
    
    -- Timestamps
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    
    UNIQUE(source_adapter, source_id)
);

CREATE INDEX idx_threads_channel ON threads(channel);
CREATE INDEX idx_threads_parent ON threads(parent_thread_id);
CREATE INDEX idx_threads_name ON threads(name);
```

**Note**: No `is_group` field. Group status is derived from event_participants - if an event has >2 participants, it's effectively a group context. A 1:1 can become a group when someone is CC'd on an email.

**Note**: Membership over time is captured in `event_participants`, not at the thread level. This handles the reality that participants change (added to email, removed from group chat, etc.).

---

## 3. Reactions (Events with content_type)

Reactions are just events with a special content_type that reference another event.

```sql
-- Example: A 👍 reaction to a message
INSERT INTO events (id, timestamp, channel, content_types, content, direction, thread_id, reply_to, ...)
VALUES (
    'evt_reaction_123',
    1705000000,
    'imessage',
    '["reaction"]',           -- content_type indicates this is a reaction
    '👍',                     -- content is the reaction emoji/type
    'sent',
    'thread_abc',
    'evt_original_message',   -- reply_to points to the reacted event
    ...
);
```

This approach:
- No new table needed
- Works for any channel (iMessage tapbacks, Slack emoji, Discord reactions)
- `reply_to` creates the reference chain
- Can query all reactions to a message: `SELECT * FROM events WHERE reply_to = 'evt_xyz' AND content_types LIKE '%reaction%'`

---

## 4. Attachments Table

Eve's attachment handling:
```sql
-- Eve stores: message_id, file_name, mime_type, size, is_sticker, guid, uti, created_date
```

Comms needs a similar dedicated table:

```sql
-- Attachments: media/file metadata for events
CREATE TABLE attachments (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    
    -- File metadata
    filename TEXT,
    mime_type TEXT,
    size_bytes INTEGER,
    
    -- Type hints
    media_type TEXT,                       -- "image", "video", "audio", "document", "sticker", "link"
    
    -- Storage location
    storage_uri TEXT,                      -- file:///path, s3://bucket/key, https://url
    storage_type TEXT,                     -- "local", "s3", "url", "inline"
    
    -- Content hash for dedup
    content_hash TEXT,
    
    -- Source tracking
    source_id TEXT,                        -- Original attachment ID from source
    
    -- Additional metadata
    metadata_json TEXT,                    -- Width/height for images, duration for video, etc.
    
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_attachments_event ON attachments(event_id);
CREATE INDEX idx_attachments_mime ON attachments(mime_type);
CREATE INDEX idx_attachments_media_type ON attachments(media_type);
CREATE INDEX idx_attachments_hash ON attachments(content_hash);
```

The `events.content_types` array indicates presence: `["text", "image"]` or `["attachment"]`.
The `attachments` table provides the details.

---

## 5. Flexible Conversation Abstraction

**Key principle**: Events are immutable raw data with no `conversation_id` FK. Conversations are a **view** over events.

### Design Goals

1. Same events can belong to **different conversations** depending on chunking strategy
2. Conversations can **span multiple channels** (iMessage + email between same people)
3. Conversations can **span multiple threads** in the same channel (two people chatting in different group chats)
4. Maximum flexibility for slicing: by thread, by channel, by person, by person-in-thread, etc.

### Schema

```sql
-- Conversation definitions: HOW to chunk events into conversations
CREATE TABLE conversation_definitions (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,             -- "imessage_90min", "gmail_thread", "cross_channel_persona"
    
    -- Scope (what events to consider)
    channel TEXT,                          -- NULL = all channels, or specific channel
    
    -- Strategy
    strategy TEXT NOT NULL,                -- "time_gap", "thread", "session", "daily", "persona_pair", "custom"
    config_json TEXT NOT NULL,             -- Strategy-specific config
    
    description TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Example definitions:
-- 
-- 1. iMessage 90-minute gaps within a thread:
--    name: "imessage_90min"
--    channel: "imessage"
--    strategy: "time_gap"
--    config_json: {"gap_seconds": 5400, "scope": "thread"}
--
-- 2. Gmail threads (use existing thread boundaries):
--    name: "gmail_thread"
--    channel: "gmail"
--    strategy: "thread"
--    config_json: {}
--
-- 3. Cross-channel persona pair (Tyler + Grace across all channels):
--    name: "tyler_grace_all_channels"
--    channel: NULL
--    strategy: "persona_pair"
--    config_json: {"person_ids": ["person_tyler", "person_grace"], "gap_seconds": 7200}
--
-- 4. Daily digest across everything:
--    name: "daily_digest"
--    channel: NULL
--    strategy: "daily"
--    config_json: {"timezone": "America/Los_Angeles"}

-- Conversations: instances produced by applying a definition
CREATE TABLE conversations (
    id TEXT PRIMARY KEY,
    definition_id TEXT NOT NULL REFERENCES conversation_definitions(id),
    
    -- Scope info (denormalized for queries)
    -- These can be NULL for cross-channel/cross-thread conversations
    channel TEXT,                          -- NULL if spans multiple channels
    thread_id TEXT REFERENCES threads(id), -- NULL if spans multiple threads
    
    -- Time bounds
    start_time INTEGER NOT NULL,
    end_time INTEGER NOT NULL,
    
    -- Stats
    event_count INTEGER NOT NULL,
    
    -- Convenience refs
    first_event_id TEXT,
    last_event_id TEXT,
    
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_conversations_definition ON conversations(definition_id);
CREATE INDEX idx_conversations_channel ON conversations(channel);
CREATE INDEX idx_conversations_thread ON conversations(thread_id);
CREATE INDEX idx_conversations_time ON conversations(start_time, end_time);

-- Conversation events: which events belong to which conversation
CREATE TABLE conversation_events (
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,             -- order within conversation (1-indexed)
    PRIMARY KEY (conversation_id, event_id)
);

CREATE INDEX idx_conversation_events_event ON conversation_events(event_id);
```

### How It Works

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                      FLEXIBLE CONVERSATION MODEL                              │
├──────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  SAME THREAD, DIFFERENT CHUNKING                                             │
│  ┌─────────────────────────┐          ┌─────────────────────────┐           │
│  │ imessage_90min          │          │ conv_abc (3 events)     │           │
│  │ gap_seconds: 5400       │──creates─▶│ thread: chat_grace      │           │
│  └─────────────────────────┘          └─────────────────────────┘           │
│                                                                              │
│  ┌─────────────────────────┐          ┌─────────────────────────┐           │
│  │ imessage_3hr            │          │ conv_def (5 events)     │           │
│  │ gap_seconds: 10800      │──creates─▶│ thread: chat_grace      │ (bigger!) │
│  └─────────────────────────┘          └─────────────────────────┘           │
│                                                                              │
│  CROSS-CHANNEL PERSONA PAIR                                                  │
│  ┌─────────────────────────┐          ┌─────────────────────────┐           │
│  │ tyler_grace_all         │          │ conv_xyz                │           │
│  │ strategy: persona_pair  │──creates─▶│ channel: NULL (multi)   │           │
│  │ persons: [tyler, grace] │          │ thread: NULL (multi)    │           │
│  └─────────────────────────┘          │ events from: imessage,  │           │
│                                       │   gmail, discord        │           │
│                                       └─────────────────────────┘           │
│                                                                              │
│  SAME CHANNEL, MULTIPLE THREADS (two people in different group chats)       │
│  ┌─────────────────────────┐          ┌─────────────────────────┐           │
│  │ tyler_grace_imessage    │          │ conv_multi_thread       │           │
│  │ channel: imessage       │──creates─▶│ channel: imessage       │           │
│  │ strategy: persona_pair  │          │ thread: NULL (multi)    │           │
│  └─────────────────────────┘          │ events from: thread_a,  │           │
│                                       │   thread_b, thread_c    │           │
│                                       └─────────────────────────┘           │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Query Examples

```sql
-- Get all events in a conversation (ordered)
SELECT e.* FROM events e
JOIN conversation_events ce ON e.id = ce.event_id
WHERE ce.conversation_id = 'conv_abc123'
ORDER BY ce.position;

-- Get conversations for a specific thread using 90min chunking
SELECT c.* FROM conversations c
WHERE c.definition_id = 'def_imsg_90m'
  AND c.thread_id = 'thread_grace';

-- Get conversations for a person in a specific channel
SELECT DISTINCT c.* FROM conversations c
JOIN conversation_events ce ON c.id = ce.conversation_id
JOIN event_participants ep ON ce.event_id = ep.event_id
WHERE c.channel = 'imessage'
  AND ep.person_id = 'person_grace';

-- Get cross-channel conversations involving two people
SELECT c.* FROM conversations c
WHERE c.definition_id = 'tyler_grace_all_channels';

-- Find which conversation(s) an event belongs to
SELECT c.*, cd.name as definition_name FROM conversations c
JOIN conversation_definitions cd ON c.definition_id = cd.id
JOIN conversation_events ce ON c.id = ce.conversation_id
WHERE ce.event_id = 'evt_xyz';
```

---

## 6. Generic Analysis Framework

### The Challenge

Eve's approach: **one table per facet type** (entities, topics, emotions, humor)
- Pro: Easy SQL queries (`SELECT * FROM entities WHERE title = 'Grace'`)
- Con: Schema changes require migrations for each new analysis type

**Goal**: Keep queryability, but make analysis types configurable without schema migrations.

### Schema

```sql
-- Analysis types: defines what kind of analysis this is
CREATE TABLE analysis_types (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,           -- "convo_all_v1", "pii_extraction_v1", "summary_v1"
    version TEXT NOT NULL,               -- "1.0.0"
    description TEXT,
    
    -- Output type
    output_type TEXT NOT NULL,           -- "structured", "freeform"
    
    -- For structured outputs: what facets to extract
    -- NULL for freeform analyses
    facets_config_json TEXT,             -- Extraction rules (see below)
    
    -- Prompt (assuming all analyses are LLM-based)
    prompt_template TEXT NOT NULL,       -- The prompt template with {conversation_text} placeholder
    model TEXT,                          -- "gemini-2.0-flash-thinking", etc.
    
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Analysis runs: one per (analysis_type, conversation) pair
CREATE TABLE analysis_runs (
    id TEXT PRIMARY KEY,
    analysis_type_id TEXT NOT NULL REFERENCES analysis_types(id),
    conversation_id TEXT NOT NULL REFERENCES conversations(id),
    
    status TEXT NOT NULL,                -- "pending", "running", "completed", "failed", "blocked"
    
    -- Timing
    started_at INTEGER,
    completed_at INTEGER,
    
    -- For freeform analyses: store the text output
    output_text TEXT,                    -- Markdown, plain text, whatever the LLM produced
    
    -- Error handling
    error_message TEXT,
    blocked_reason TEXT,
    retry_count INTEGER DEFAULT 0,
    
    created_at INTEGER NOT NULL,
    
    UNIQUE(analysis_type_id, conversation_id)
);

CREATE INDEX idx_analysis_runs_type ON analysis_runs(analysis_type_id);
CREATE INDEX idx_analysis_runs_conversation ON analysis_runs(conversation_id);
CREATE INDEX idx_analysis_runs_status ON analysis_runs(status);

-- Facets: extracted queryable values from structured analyses
CREATE TABLE facets (
    id TEXT PRIMARY KEY,
    analysis_run_id TEXT NOT NULL REFERENCES analysis_runs(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    
    -- What kind of facet
    facet_type TEXT NOT NULL,            -- "entity", "topic", "emotion", "pii_email", "summary", etc.
    
    -- The extracted value
    value TEXT NOT NULL,                 -- "Grace", "travel", "joy", "grace@example.com"
    
    -- Attribution (optional - who mentioned this)
    person_id TEXT REFERENCES persons(id),
    
    -- Confidence/metadata
    confidence REAL,
    metadata_json TEXT,                  -- Additional context
    
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_facets_type_value ON facets(facet_type, value);
CREATE INDEX idx_facets_conversation ON facets(conversation_id);
CREATE INDEX idx_facets_analysis_run ON facets(analysis_run_id);
CREATE INDEX idx_facets_person ON facets(person_id);
CREATE INDEX idx_facets_value ON facets(value);
```

### Understanding `facets_config_json`

This tells the system how to extract queryable facets from LLM output.

**Example for convo_all_v1** (extracts entities, topics, emotions):

```json
{
  "response_format": "json",
  "json_schema": {
    "type": "object",
    "properties": {
      "summary": {"type": "string"},
      "entities": {
        "type": "array",
        "items": {
          "type": "object",
          "properties": {
            "participant_name": {"type": "string"},
            "entities": {"type": "array", "items": {"type": "object", "properties": {"name": {"type": "string"}}}}
          }
        }
      },
      "topics": { "...similar structure..." },
      "emotions": { "...similar structure..." }
    }
  },
  "extractions": [
    {"facet_type": "entity", "json_path": "$.entities[*].entities[*].name", "person_path": "$.entities[*].participant_name"},
    {"facet_type": "topic", "json_path": "$.topics[*].topics[*].name", "person_path": "$.topics[*].participant_name"},
    {"facet_type": "emotion", "json_path": "$.emotions[*].emotions[*].name", "person_path": "$.emotions[*].participant_name"},
    {"facet_type": "summary", "json_path": "$.summary"}
  ]
}
```

**Example for pii_extraction_v1** (extracts emails, phones, names):

```json
{
  "response_format": "json",
  "json_schema": { "...pii schema..." },
  "extractions": [
    {"facet_type": "pii_email", "json_path": "$.pii[*].emails[*]", "person_path": "$.pii[*].person_name"},
    {"facet_type": "pii_phone", "json_path": "$.pii[*].phones[*]", "person_path": "$.pii[*].person_name"},
    {"facet_type": "pii_full_name", "json_path": "$.pii[*].full_name", "person_path": "$.pii[*].person_name"}
  ]
}
```

**Example for freeform analysis** (no facet extraction):

```json
null
```

When `facets_config_json` is null and `output_type` is "freeform", the LLM output is stored directly in `analysis_runs.output_text` as-is. No facet extraction happens.

### Why We Don't Store Raw Completions

**Question**: Do we need to store the raw LLM JSON output?

**Answer**: No, we only store facets.

- **Facets ARE the parsed output** - they're the queryable, usable data
- **Raw JSON is reconstructable** from facets if needed (group by conversation_id, facet_type)
- **Saves storage** - we don't duplicate data
- **Simpler queries** - one table to query, not JSON parsing

For **freeform analyses** (like generating a summary .md file), the output goes in `analysis_runs.output_text`.

### Flow Diagram

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                         ANALYSIS FLOW                                         │
├──────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  STRUCTURED ANALYSIS (convo_all_v1)                                          │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐    ┌─────────────┐   │
│  │ Conversation │───▶│ LLM Prompt  │───▶│ JSON Output │───▶│ Facets      │   │
│  │ conv_abc     │    │ + Schema    │    │ {entities,  │    │ entity:Grace│   │
│  └─────────────┘    └─────────────┘    │  topics...} │    │ topic:travel│   │
│                                        └─────────────┘    │ emotion:joy │   │
│                                        (discarded after   └─────────────┘   │
│                                         facet extraction)  (persisted)      │
│                                                                              │
│  FREEFORM ANALYSIS (weekly_summary_v1)                                       │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐                      │
│  │ Conversation │───▶│ LLM Prompt  │───▶│ output_text │                      │
│  │ conv_abc     │    │ (no schema) │    │ "# Summary  │                      │
│  └─────────────┘    └─────────────┘    │  This week  │                      │
│                                        │  you..."    │                      │
│                                        └─────────────┘                      │
│                                        (persisted in                        │
│                                         analysis_runs.output_text)          │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Query Examples

```sql
-- Find all conversations mentioning 'Grace' as an entity
SELECT DISTINCT c.* FROM conversations c
JOIN facets f ON c.id = f.conversation_id
WHERE f.facet_type = 'entity' AND f.value = 'Grace';

-- Find conversations with both 'Grace' and 'travel'
SELECT c.* FROM conversations c
WHERE EXISTS (SELECT 1 FROM facets f WHERE f.conversation_id = c.id 
              AND f.facet_type = 'entity' AND f.value = 'Grace')
  AND EXISTS (SELECT 1 FROM facets f WHERE f.conversation_id = c.id 
              AND f.facet_type = 'topic' AND f.value = 'travel');

-- Get all extracted emails (from PII analysis)
SELECT f.value, f.person_id, p.canonical_name
FROM facets f
LEFT JOIN persons p ON f.person_id = p.id
WHERE f.facet_type = 'pii_email';

-- Get freeform analysis output for a conversation
SELECT ar.output_text FROM analysis_runs ar
JOIN analysis_types at ON ar.analysis_type_id = at.id
WHERE ar.conversation_id = 'conv_abc' 
  AND at.output_type = 'freeform';

-- Get summary facet for a conversation
SELECT f.value FROM facets f
WHERE f.conversation_id = 'conv_abc' AND f.facet_type = 'summary';

-- List all facet types and counts
SELECT facet_type, COUNT(*) as count FROM facets GROUP BY facet_type ORDER BY count DESC;
```

### Adding a New Analysis Type

```sql
-- Example: Adding a "key_moments" analysis that extracts important moments
INSERT INTO analysis_types (id, name, version, description, output_type, facets_config_json, prompt_template, model, created_at, updated_at)
VALUES (
    'key_moments_v1',
    'key_moments',
    '1.0.0',
    'Extracts key moments and turning points from a conversation',
    'structured',
    '{
        "response_format": "json",
        "extractions": [
            {"facet_type": "key_moment", "json_path": "$.moments[*].description"},
            {"facet_type": "key_moment_timestamp", "json_path": "$.moments[*].approximate_time"}
        ]
    }',
    'Analyze this conversation and identify key moments...\n\n{conversation_text}',
    'gemini-2.0-flash',
    strftime(''%s'', ''now''),
    strftime(''%s'', ''now'')
);

-- No schema migration! Just run the analysis and facets auto-populate.
```

### Example: Freeform Analysis (Weekly Summary)

```sql
INSERT INTO analysis_types (id, name, version, description, output_type, facets_config_json, prompt_template, model, created_at, updated_at)
VALUES (
    'weekly_narrative_v1',
    'weekly_narrative',
    '1.0.0',
    'Generates a narrative summary of the week''s conversations',
    'freeform',
    NULL,  -- No facet extraction
    'Write a narrative summary of these conversations as if writing in a journal...\n\n{conversation_text}',
    'gemini-2.0-flash-thinking',
    strftime(''%s'', ''now''),
    strftime(''%s'', ''now'')
);

-- Output stored in analysis_runs.output_text as markdown/plain text
```

---

## 7. Embeddings (Simple)

Embeddings are straightforward: text → vector. Can attach to anything.

```sql
CREATE TABLE embeddings (
    id TEXT PRIMARY KEY,
    
    -- What is embedded
    entity_type TEXT NOT NULL,           -- "event", "conversation", "facet", "person", "thread"
    entity_id TEXT NOT NULL,             -- ID of the embedded entity
    
    -- The embedding
    model TEXT NOT NULL,                 -- "gemini-embedding-004", etc.
    embedding_blob BLOB NOT NULL,        -- Binary vector (little-endian float64 array)
    dimension INTEGER NOT NULL,          -- 768, 1024, etc.
    
    -- Source text hash (for change detection / re-embedding)
    source_text_hash TEXT,
    
    created_at INTEGER NOT NULL,
    
    UNIQUE(entity_type, entity_id, model)
);

CREATE INDEX idx_embeddings_entity ON embeddings(entity_type, entity_id);
CREATE INDEX idx_embeddings_model ON embeddings(model);
```

### Embedding Targets

| entity_type | entity_id | What's embedded |
|-------------|-----------|-----------------|
| `event` | events.id | Single message text |
| `conversation` | conversations.id | Concatenated/encoded conversation text |
| `facet` | facets.id | The facet value (for semantic facet search) |
| `person` | persons.id | Aggregated info about a person |
| `thread` | threads.id | Thread name/context |

---

## 8. Complete Schema: New Additions to Comms

```sql
-- ============================================================================
-- THREADS (chat/channel metadata)
-- ============================================================================

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

CREATE INDEX idx_threads_channel ON threads(channel);
CREATE INDEX idx_threads_parent ON threads(parent_thread_id);
CREATE INDEX idx_threads_name ON threads(name);

-- ============================================================================
-- ATTACHMENTS
-- ============================================================================

CREATE TABLE attachments (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    filename TEXT,
    mime_type TEXT,
    size_bytes INTEGER,
    media_type TEXT,
    storage_uri TEXT,
    storage_type TEXT,
    content_hash TEXT,
    source_id TEXT,
    metadata_json TEXT,
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_attachments_event ON attachments(event_id);
CREATE INDEX idx_attachments_mime ON attachments(mime_type);
CREATE INDEX idx_attachments_media_type ON attachments(media_type);
CREATE INDEX idx_attachments_hash ON attachments(content_hash);

-- ============================================================================
-- CONVERSATION ABSTRACTION
-- ============================================================================

CREATE TABLE conversation_definitions (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    channel TEXT,
    strategy TEXT NOT NULL,
    config_json TEXT NOT NULL,
    description TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE conversations (
    id TEXT PRIMARY KEY,
    definition_id TEXT NOT NULL REFERENCES conversation_definitions(id),
    channel TEXT,
    thread_id TEXT REFERENCES threads(id),
    start_time INTEGER NOT NULL,
    end_time INTEGER NOT NULL,
    event_count INTEGER NOT NULL,
    first_event_id TEXT,
    last_event_id TEXT,
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_conversations_definition ON conversations(definition_id);
CREATE INDEX idx_conversations_channel ON conversations(channel);
CREATE INDEX idx_conversations_thread ON conversations(thread_id);
CREATE INDEX idx_conversations_time ON conversations(start_time, end_time);

CREATE TABLE conversation_events (
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    PRIMARY KEY (conversation_id, event_id)
);

CREATE INDEX idx_conversation_events_event ON conversation_events(event_id);

-- ============================================================================
-- GENERIC ANALYSIS FRAMEWORK
-- ============================================================================

CREATE TABLE analysis_types (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    version TEXT NOT NULL,
    description TEXT,
    output_type TEXT NOT NULL,
    facets_config_json TEXT,
    prompt_template TEXT NOT NULL,
    model TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE analysis_runs (
    id TEXT PRIMARY KEY,
    analysis_type_id TEXT NOT NULL REFERENCES analysis_types(id),
    conversation_id TEXT NOT NULL REFERENCES conversations(id),
    status TEXT NOT NULL,
    started_at INTEGER,
    completed_at INTEGER,
    output_text TEXT,
    error_message TEXT,
    blocked_reason TEXT,
    retry_count INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL,
    UNIQUE(analysis_type_id, conversation_id)
);

CREATE INDEX idx_analysis_runs_type ON analysis_runs(analysis_type_id);
CREATE INDEX idx_analysis_runs_conversation ON analysis_runs(conversation_id);
CREATE INDEX idx_analysis_runs_status ON analysis_runs(status);

CREATE TABLE facets (
    id TEXT PRIMARY KEY,
    analysis_run_id TEXT NOT NULL REFERENCES analysis_runs(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    facet_type TEXT NOT NULL,
    value TEXT NOT NULL,
    person_id TEXT REFERENCES persons(id),
    confidence REAL,
    metadata_json TEXT,
    created_at INTEGER NOT NULL
);

CREATE INDEX idx_facets_type_value ON facets(facet_type, value);
CREATE INDEX idx_facets_conversation ON facets(conversation_id);
CREATE INDEX idx_facets_analysis_run ON facets(analysis_run_id);
CREATE INDEX idx_facets_person ON facets(person_id);
CREATE INDEX idx_facets_value ON facets(value);

-- ============================================================================
-- EMBEDDINGS
-- ============================================================================

CREATE TABLE embeddings (
    id TEXT PRIMARY KEY,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    model TEXT NOT NULL,
    embedding_blob BLOB NOT NULL,
    dimension INTEGER NOT NULL,
    source_text_hash TEXT,
    created_at INTEGER NOT NULL,
    UNIQUE(entity_type, entity_id, model)
);

CREATE INDEX idx_embeddings_entity ON embeddings(entity_type, entity_id);
CREATE INDEX idx_embeddings_model ON embeddings(model);
```

### Existing events table modification

```sql
-- Update events.thread_id to reference threads table (optional - can keep as TEXT for flexibility)
-- Reactions: use content_types = '["reaction"]' and reply_to = reacted_event_id
```

---

## 9. Migration Path

### Phase 1: Schema Additions
1. Add `threads` table
2. Add `attachments` table
3. Add conversation abstraction tables (`conversation_definitions`, `conversations`, `conversation_events`)
4. Add analysis framework tables (`analysis_types`, `analysis_runs`, `facets`)
5. Add `embeddings` table

### Phase 2: Eve Direct ETL
1. Modify Eve adapter to read from chat.db directly (instead of eve.db)
2. Create threads from Eve chats
3. Import attachments with metadata
4. Handle reactions as events with `content_types: ["reaction"]`

### Phase 3: Conversation Chunking
1. Implement chunking strategies (time_gap, thread, session, daily, persona_pair)
2. Seed default conversation_definitions
3. Run initial chunking on existing events

### Phase 4: Analysis Migration
1. Register `convo_all_v1` as analysis_type with facets_config_json
2. Port analysis job handler from Eve
3. Implement facet extraction post-processor
4. Migrate existing Eve analysis data → facets format

### Phase 5: Embeddings Migration
1. Port embedding job handler from Eve
2. Migrate existing Eve embeddings → comms format

---

## 10. Summary

| Concern | Solution |
|---------|----------|
| **Chat/channel metadata** | `threads` table - generic container |
| **Dynamic membership** | `event_participants` per-event (no is_group needed) |
| **Reactions** | Events with `content_types: ["reaction"]`, `reply_to` refs target |
| **Attachments** | Dedicated `attachments` table with full metadata |
| **Flexible conversations** | `conversation_definitions` + `conversations` + `conversation_events` |
| **Cross-channel convos** | Supported - `conversations.channel` and `thread_id` can be NULL |
| **Cross-thread convos** | Supported - same mechanism |
| **Generic analysis** | `analysis_types` + `analysis_runs` + `facets` |
| **Structured output** | Parsed into `facets` table (queryable) |
| **Freeform output** | Stored in `analysis_runs.output_text` |
| **Embeddings** | `embeddings` table - attach to anything |

**Ready to implement!**
