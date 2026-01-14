-- Schema version tracking
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);

-- Events: All communication events across channels
CREATE TABLE IF NOT EXISTS events (
    id TEXT PRIMARY KEY,
    timestamp INTEGER NOT NULL,
    channel TEXT NOT NULL,
    content_types TEXT NOT NULL,  -- JSON array: ["text"], ["text", "image"]
    content TEXT,
    direction TEXT NOT NULL,      -- sent, received, observed
    thread_id TEXT,
    reply_to TEXT,
    source_adapter TEXT NOT NULL,
    source_id TEXT NOT NULL,
    UNIQUE(source_adapter, source_id)
);

CREATE INDEX IF NOT EXISTS idx_events_timestamp ON events(timestamp);
CREATE INDEX IF NOT EXISTS idx_events_channel ON events(channel);
CREATE INDEX IF NOT EXISTS idx_events_thread ON events(thread_id);

-- Persons: People with unified identity
CREATE TABLE IF NOT EXISTS persons (
    id TEXT PRIMARY KEY,
    canonical_name TEXT NOT NULL,
    display_name TEXT,
    is_me INTEGER DEFAULT 0,
    relationship_type TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_persons_is_me ON persons(is_me);
CREATE INDEX IF NOT EXISTS idx_persons_canonical_name ON persons(canonical_name);

-- Identities: Identifiers (phone, email, handle) linked to persons
CREATE TABLE IF NOT EXISTS identities (
    id TEXT PRIMARY KEY,
    person_id TEXT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    channel TEXT NOT NULL,
    identifier TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(channel, identifier)
);

CREATE INDEX IF NOT EXISTS idx_identities_person ON identities(person_id);
CREATE INDEX IF NOT EXISTS idx_identities_identifier ON identities(channel, identifier);

-- Event Participants: Who was involved in each event
CREATE TABLE IF NOT EXISTS event_participants (
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    person_id TEXT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    role TEXT NOT NULL,  -- sender, recipient, cc, observer
    PRIMARY KEY (event_id, person_id, role)
);

CREATE INDEX IF NOT EXISTS idx_event_participants_event ON event_participants(event_id);
CREATE INDEX IF NOT EXISTS idx_event_participants_person ON event_participants(person_id);

-- Event state: Channel-agnostic mutable state for messages
CREATE TABLE IF NOT EXISTS event_state (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    read_state TEXT NOT NULL DEFAULT 'unknown', -- unknown|read|unread
    flagged INTEGER NOT NULL DEFAULT 0,
    archived INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'sent', -- draft|sent|received|failed|deleted|unknown
    updated_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_event_state_read_state ON event_state(read_state);
CREATE INDEX IF NOT EXISTS idx_event_state_flagged ON event_state(flagged);
CREATE INDEX IF NOT EXISTS idx_event_state_archived ON event_state(archived);

-- Event tags: Channel-agnostic tags (distinct from analysis tags table above)
CREATE TABLE IF NOT EXISTS event_tags (
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    tag TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT 'system', -- system|user|analysis|gmail|imessage|...
    created_at INTEGER NOT NULL,
    PRIMARY KEY (event_id, tag, source)
);

CREATE INDEX IF NOT EXISTS idx_event_tags_event ON event_tags(event_id);
CREATE INDEX IF NOT EXISTS idx_event_tags_tag ON event_tags(tag);

-- Tags: Soft tags on events for categorization
CREATE TABLE IF NOT EXISTS tags (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    tag_type TEXT NOT NULL,    -- topic, entity, emotion, project, context
    value TEXT NOT NULL,
    confidence REAL,
    source TEXT NOT NULL,      -- user, analysis
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tags_event ON tags(event_id);
CREATE INDEX IF NOT EXISTS idx_tags_type_value ON tags(tag_type, value);

-- Threads: Grouping containers for events (chats, email threads, channels, sessions)
CREATE TABLE IF NOT EXISTS threads (
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

CREATE INDEX IF NOT EXISTS idx_threads_channel ON threads(channel);
CREATE INDEX IF NOT EXISTS idx_threads_parent ON threads(parent_thread_id);
CREATE INDEX IF NOT EXISTS idx_threads_name ON threads(name);

-- Attachments: Media/file metadata for events
CREATE TABLE IF NOT EXISTS attachments (
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

CREATE INDEX IF NOT EXISTS idx_attachments_event ON attachments(event_id);
CREATE INDEX IF NOT EXISTS idx_attachments_mime ON attachments(mime_type);
CREATE INDEX IF NOT EXISTS idx_attachments_media_type ON attachments(media_type);
CREATE INDEX IF NOT EXISTS idx_attachments_hash ON attachments(content_hash);

-- Sync watermarks: Track last sync per adapter
CREATE TABLE IF NOT EXISTS sync_watermarks (
    adapter TEXT PRIMARY KEY,
    last_sync_at INTEGER NOT NULL,
    last_event_id TEXT
);

-- Adapter state: generic key/value store for adapter-specific durable state
CREATE TABLE IF NOT EXISTS adapter_state (
    adapter TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (adapter, key)
);

-- Bus events: append-only event stream for downstream automation
CREATE TABLE IF NOT EXISTS bus_events (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    type TEXT NOT NULL,
    adapter TEXT,
    comms_event_id TEXT,
    created_at INTEGER NOT NULL,
    payload_json TEXT
);

CREATE INDEX IF NOT EXISTS idx_bus_events_created_at ON bus_events(created_at);
CREATE INDEX IF NOT EXISTS idx_bus_events_type ON bus_events(type);
CREATE INDEX IF NOT EXISTS idx_bus_events_event ON bus_events(comms_event_id);

-- Sync jobs: Track background/resumable sync progress per adapter
CREATE TABLE IF NOT EXISTS sync_jobs (
    adapter TEXT PRIMARY KEY,
    status TEXT NOT NULL,      -- running, success, error
    phase TEXT NOT NULL,       -- sync, recent, backfill, incremental, history
    cursor TEXT,               -- opaque cursor (e.g., backfill:YYYY-MM-DD)
    started_at INTEGER,
    updated_at INTEGER NOT NULL,
    last_error TEXT,
    progress_json TEXT         -- JSON blob with counters, ETA, etc.
);

-- Merge suggestions: Proposed identity merges for user review
-- Generated from fuzzy evidence (name similarity, shared domains) rather than
-- deterministic matches (exact email/phone overlap which auto-merge).
CREATE TABLE IF NOT EXISTS merge_suggestions (
    id TEXT PRIMARY KEY,
    person1_id TEXT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    person2_id TEXT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,
    evidence_type TEXT NOT NULL,   -- name_similarity, shared_domain, co_occurrence
    evidence_json TEXT,            -- details about why this match was suggested
    confidence REAL NOT NULL,      -- 0.0-1.0 score
    person1_event_count INTEGER,   -- cached for prioritization
    person2_event_count INTEGER,
    status TEXT NOT NULL DEFAULT 'pending', -- pending, accepted, rejected, expired
    created_at INTEGER NOT NULL,
    reviewed_at INTEGER,
    UNIQUE(person1_id, person2_id)
);

CREATE INDEX IF NOT EXISTS idx_merge_suggestions_status ON merge_suggestions(status);
CREATE INDEX IF NOT EXISTS idx_merge_suggestions_confidence ON merge_suggestions(confidence DESC);
CREATE INDEX IF NOT EXISTS idx_merge_suggestions_person1 ON merge_suggestions(person1_id);
CREATE INDEX IF NOT EXISTS idx_merge_suggestions_person2 ON merge_suggestions(person2_id);

-- Person facts: Rich identity graph data extracted from conversations
-- Stores all PII and enrichment data with attribution and confidence
CREATE TABLE IF NOT EXISTS person_facts (
    id TEXT PRIMARY KEY,
    person_id TEXT NOT NULL REFERENCES persons(id) ON DELETE CASCADE,

    -- What fact
    category TEXT NOT NULL,         -- 'core_identity', 'contact', 'professional', etc.
    fact_type TEXT NOT NULL,        -- 'email_work', 'full_name', 'business_owned', etc.
    fact_value TEXT NOT NULL,       -- the actual value

    -- Confidence & Source
    confidence REAL DEFAULT 0.5,    -- 0.0-1.0
    source_type TEXT NOT NULL,      -- 'self_disclosed', 'mentioned', 'inferred', 'signature'
    source_channel TEXT,            -- 'imessage', 'gmail', etc.
    source_conversation_id TEXT,    -- conversation where extracted
    source_facet_id TEXT,           -- link to facets table
    evidence TEXT,                  -- quote from message

    -- Classification
    is_sensitive INTEGER DEFAULT 0,       -- SSN, medical, financial
    is_identifier INTEGER DEFAULT 0,      -- used for identity matching
    is_hard_identifier INTEGER DEFAULT 0, -- triggers instant merge consideration

    -- Timestamps
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,

    UNIQUE(person_id, category, fact_type, fact_value)
);

CREATE INDEX IF NOT EXISTS idx_person_facts_person ON person_facts(person_id);
CREATE INDEX IF NOT EXISTS idx_person_facts_type ON person_facts(category, fact_type);
CREATE INDEX IF NOT EXISTS idx_person_facts_value ON person_facts(fact_value);
CREATE INDEX IF NOT EXISTS idx_person_facts_hard_id ON person_facts(fact_type, fact_value)
    WHERE is_hard_identifier = 1;

-- Unattributed facts: Facts extracted from conversations that couldn't be attributed to a specific person
-- For example: phone numbers shared without context about whose number it is
CREATE TABLE IF NOT EXISTS unattributed_facts (
    id TEXT PRIMARY KEY,
    fact_type TEXT NOT NULL,
    fact_value TEXT NOT NULL,

    shared_by_person_id TEXT REFERENCES persons(id),
    source_event_id TEXT REFERENCES events(id),
    source_conversation_id TEXT REFERENCES conversations(id),
    context TEXT,
    possible_attributions TEXT,     -- JSON array of guesses

    resolved_to_person_id TEXT REFERENCES persons(id),
    resolution_evidence TEXT,

    created_at INTEGER NOT NULL,
    resolved_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_unattributed_value ON unattributed_facts(fact_type, fact_value);
CREATE INDEX IF NOT EXISTS idx_unattributed_unresolved ON unattributed_facts(resolved_to_person_id)
    WHERE resolved_to_person_id IS NULL;

-- Merge events: Proposed and executed identity merges
-- Tracks both pending suggestions and completed merges with full audit trail
CREATE TABLE IF NOT EXISTS merge_events (
    id TEXT PRIMARY KEY,
    source_person_id TEXT NOT NULL REFERENCES persons(id),
    target_person_id TEXT NOT NULL REFERENCES persons(id),
    merge_type TEXT NOT NULL,        -- 'hard_identifier', 'compound', 'soft_accumulation', 'manual'

    triggering_facts TEXT,           -- JSON array of facts that caused merge
    similarity_score REAL,

    status TEXT DEFAULT 'pending',   -- 'pending', 'accepted', 'rejected', 'executed'
    auto_eligible INTEGER DEFAULT 0, -- whether this can auto-execute

    created_at INTEGER NOT NULL,
    resolved_at INTEGER,
    resolved_by TEXT                 -- 'auto' or user identifier
);

CREATE INDEX IF NOT EXISTS idx_merge_events_status ON merge_events(status);
CREATE INDEX IF NOT EXISTS idx_merge_events_persons ON merge_events(source_person_id, target_person_id);

-- Conversation definitions: HOW to chunk events into conversations
CREATE TABLE IF NOT EXISTS conversation_definitions (
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

-- Conversations: instances produced by applying a definition
CREATE TABLE IF NOT EXISTS conversations (
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

CREATE INDEX IF NOT EXISTS idx_conversations_definition ON conversations(definition_id);
CREATE INDEX IF NOT EXISTS idx_conversations_channel ON conversations(channel);
CREATE INDEX IF NOT EXISTS idx_conversations_thread ON conversations(thread_id);
CREATE INDEX IF NOT EXISTS idx_conversations_time ON conversations(start_time, end_time);

-- Conversation events: which events belong to which conversation
CREATE TABLE IF NOT EXISTS conversation_events (
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,             -- order within conversation (1-indexed)
    PRIMARY KEY (conversation_id, event_id)
);

CREATE INDEX IF NOT EXISTS idx_conversation_events_event ON conversation_events(event_id);

-- Analysis types: defines what kind of analysis this is
CREATE TABLE IF NOT EXISTS analysis_types (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,           -- "convo_all_v1", "pii_extraction_v1", "summary_v1"
    version TEXT NOT NULL,               -- "1.0.0"
    description TEXT,

    -- Output type
    output_type TEXT NOT NULL,           -- "structured", "freeform"

    -- For structured outputs: what facets to extract
    -- NULL for freeform analyses
    facets_config_json TEXT,             -- Extraction rules

    -- Prompt (assuming all analyses are LLM-based)
    prompt_template TEXT NOT NULL,       -- The prompt template with {conversation_text} placeholder
    model TEXT,                          -- "gemini-2.0-flash-thinking", etc.

    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Analysis runs: one per (analysis_type, conversation) pair
CREATE TABLE IF NOT EXISTS analysis_runs (
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

CREATE INDEX IF NOT EXISTS idx_analysis_runs_type ON analysis_runs(analysis_type_id);
CREATE INDEX IF NOT EXISTS idx_analysis_runs_conversation ON analysis_runs(conversation_id);
CREATE INDEX IF NOT EXISTS idx_analysis_runs_status ON analysis_runs(status);

-- Facets: extracted queryable values from structured analyses
CREATE TABLE IF NOT EXISTS facets (
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

CREATE INDEX IF NOT EXISTS idx_facets_type_value ON facets(facet_type, value);
CREATE INDEX IF NOT EXISTS idx_facets_conversation ON facets(conversation_id);
CREATE INDEX IF NOT EXISTS idx_facets_analysis_run ON facets(analysis_run_id);
CREATE INDEX IF NOT EXISTS idx_facets_person ON facets(person_id);
CREATE INDEX IF NOT EXISTS idx_facets_value ON facets(value);

-- Embeddings: Vector embeddings for entities
CREATE TABLE IF NOT EXISTS embeddings (
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

CREATE INDEX IF NOT EXISTS idx_embeddings_entity ON embeddings(entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_embeddings_model ON embeddings(model);

-- Insert initial schema version
INSERT OR IGNORE INTO schema_version (version, applied_at)
VALUES (9, strftime('%s', 'now'));

-- Register pii_extraction_v1 analysis type
INSERT OR IGNORE INTO analysis_types (
    id,
    name,
    version,
    description,
    output_type,
    facets_config_json,
    prompt_template,
    model,
    created_at,
    updated_at
) VALUES (
    'pii_extraction_v1',
    'pii_extraction',
    '1.0.0',
    'Extract ALL personally identifiable information from conversations. Creates identity graphs for each person mentioned, enabling cross-channel identity resolution through identifier collisions.',
    'structured',
    '{
        "extractions": [
            {"facet_type": "pii_email_personal", "json_path": "$.persons[*].pii.contact_information.email_personal.value", "category": "contact_information", "fact_type": "email_personal"},
            {"facet_type": "pii_email_work", "json_path": "$.persons[*].pii.contact_information.email_work.value", "category": "contact_information", "fact_type": "email_work"},
            {"facet_type": "pii_email_school", "json_path": "$.persons[*].pii.contact_information.email_school.value", "category": "contact_information", "fact_type": "email_school"},
            {"facet_type": "pii_phone_mobile", "json_path": "$.persons[*].pii.contact_information.phone_mobile.value", "category": "contact_information", "fact_type": "phone_mobile"},
            {"facet_type": "pii_phone_home", "json_path": "$.persons[*].pii.contact_information.phone_home.value", "category": "contact_information", "fact_type": "phone_home"},
            {"facet_type": "pii_phone_work", "json_path": "$.persons[*].pii.contact_information.phone_work.value", "category": "contact_information", "fact_type": "phone_work"},
            {"facet_type": "pii_full_legal_name", "json_path": "$.persons[*].pii.core_identity.full_legal_name.value", "category": "core_identity", "fact_type": "full_legal_name"},
            {"facet_type": "pii_given_name", "json_path": "$.persons[*].pii.core_identity.given_name.value", "category": "core_identity", "fact_type": "given_name"},
            {"facet_type": "pii_family_name", "json_path": "$.persons[*].pii.core_identity.family_name.value", "category": "core_identity", "fact_type": "family_name"},
            {"facet_type": "pii_middle_name", "json_path": "$.persons[*].pii.core_identity.middle_name.value", "category": "core_identity", "fact_type": "middle_name"},
            {"facet_type": "pii_nicknames", "json_path": "$.persons[*].pii.core_identity.nicknames.value", "category": "core_identity", "fact_type": "nicknames"},
            {"facet_type": "pii_birthdate", "json_path": "$.persons[*].pii.core_identity.date_of_birth.value", "category": "core_identity", "fact_type": "birthdate"},
            {"facet_type": "pii_employer_current", "json_path": "$.persons[*].pii.professional.employer_current.value", "category": "professional", "fact_type": "employer_current"},
            {"facet_type": "pii_business_owned", "json_path": "$.persons[*].pii.professional.business_owned.value", "category": "professional", "fact_type": "business_owned"},
            {"facet_type": "pii_business_role", "json_path": "$.persons[*].pii.professional.business_role.value", "category": "professional", "fact_type": "business_role"},
            {"facet_type": "pii_profession", "json_path": "$.persons[*].pii.professional.profession.value", "category": "professional", "fact_type": "profession"},
            {"facet_type": "pii_location_current", "json_path": "$.persons[*].pii.location_presence.location_current.value", "category": "location_presence", "fact_type": "location_current"},
            {"facet_type": "pii_spouse_first_name", "json_path": "$.persons[*].pii.relationships.spouse.value", "category": "relationships", "fact_type": "spouse_first_name"},
            {"facet_type": "pii_school_attended", "json_path": "$.persons[*].pii.education.school_previous.value", "category": "education", "fact_type": "school_attended"},
            {"facet_type": "pii_social_twitter", "json_path": "$.persons[*].pii.digital_identity.social_twitter.value", "category": "digital_identity", "fact_type": "social_twitter"},
            {"facet_type": "pii_social_instagram", "json_path": "$.persons[*].pii.digital_identity.social_instagram.value", "category": "digital_identity", "fact_type": "social_instagram"},
            {"facet_type": "pii_social_linkedin", "json_path": "$.persons[*].pii.digital_identity.social_linkedin.value", "category": "digital_identity", "fact_type": "social_linkedin"},
            {"facet_type": "pii_social_facebook", "json_path": "$.persons[*].pii.digital_identity.social_facebook.value", "category": "digital_identity", "fact_type": "social_facebook"},
            {"facet_type": "pii_username_generic", "json_path": "$.persons[*].pii.digital_identity.username_unknown.value", "category": "digital_identity", "fact_type": "username_generic"},
            {"facet_type": "pii_ssn", "json_path": "$.persons[*].pii.government_legal_ids.ssn.value", "category": "government_legal_ids", "fact_type": "ssn"},
            {"facet_type": "pii_passport_number", "json_path": "$.persons[*].pii.government_legal_ids.passport_number.value", "category": "government_legal_ids", "fact_type": "passport_number"},
            {"facet_type": "pii_drivers_license", "json_path": "$.persons[*].pii.government_legal_ids.drivers_license.value", "category": "government_legal_ids", "fact_type": "drivers_license"}
        ]
    }',
    '# PII Extraction Prompt v1

## Purpose

Extract ALL personally identifiable information from a conversation chunk. This creates a comprehensive identity graph for each person mentioned, enabling cross-channel identity resolution through identifier collisions.

## Input

- **Channel**: The communication channel (iMessage, Gmail, Discord, etc.)
- **Primary Contact**: The person whose conversation this is (name + identifier)
- **User**: The owner of the comms database
- **Messages**: A chunk of conversation (typically 50-100 messages or a logical conversation unit)

## Task

Extract ALL PII for EVERY person mentioned in this conversation:
1. **The primary contact** - the person the user is communicating with
2. **The user themselves** - any PII about the user mentioned in the conversation
3. **Third parties** - any other people mentioned (family, friends, colleagues, etc.)

For each piece of information:
- Quote the exact evidence from the messages
- Indicate confidence level (high/medium/low)
- Note whether this is self-disclosed or mentioned by someone else

## Important Rules

1. **Extract EVERYTHING** - Even small details can help with identity resolution later
2. **Quote exact evidence** - Always include the message text that supports each extraction
3. **Attribute correctly** - Be very careful about WHO each piece of PII belongs to
4. **Flag sensitive data** - Mark SSN, financial, medical info as sensitive
5. **Note self-disclosure** - Mark when someone explicitly shares their own info vs being mentioned
6. **Create new identity candidates** - If a third party is mentioned with enough detail, flag them
7. **Use unattributed_facts** - If an identifier (phone, email) is shared without clear ownership, put it in unattributed_facts with context about who shared it and possible attributions
8. **Owner vs Employer** - Distinguish between working FOR a company (employer_current) vs OWNING a business (business_owned). Someone who owns a restaurant is NOT employed BY it.
9. **Confidence levels**:
   - **high**: Explicitly stated or very clear
   - **medium**: Strongly implied or partially stated
   - **low**: Inferred or uncertain
10. **Don''t hallucinate** - Only extract what''s actually in the messages

## Output Format

Return a JSON object with the following structure:

```json
{
  "extraction_metadata": {
    "channel": "iMessage",
    "primary_contact_name": "Dad",
    "primary_contact_identifier": "+16508238440",
    "user_name": "Tyler Brandt",
    "message_count": 50,
    "date_range": {
      "start": "2024-01-01T00:00:00Z",
      "end": "2024-01-15T23:59:59Z"
    }
  },
  "persons": [
    {
      "reference": "Dad",
      "is_primary_contact": true,
      "confidence_is_primary": 0.99,
      "pii": {
        "core_identity": {
          "full_legal_name": {
            "value": "James Brandt",
            "confidence": "high",
            "evidence": ["meeting up with Jim and Janet", "Jim@napageneralstore.com"],
            "source": "inferred from email + nickname"
          },
          "given_name": {
            "value": "Jim",
            "confidence": "high",
            "evidence": ["refers to self as Jim"]
          },
          "family_name": {
            "value": "Brandt",
            "confidence": "high",
            "evidence": ["Jim@napageneralstore.com"]
          },
          "nicknames": {
            "value": ["Jim", "Dad"],
            "confidence": "high",
            "evidence": ["labeled as Dad in contacts", "refers to self as Jim"]
          },
          "date_of_birth": {
            "value": "1959-03-15",
            "confidence": "medium",
            "evidence": ["birthday mentioned in context"]
          }
        },
        "contact_information": {
          "email_work": {
            "value": "jim@napageneralstore.com",
            "confidence": "high",
            "evidence": ["the recovery email is my jim@napageneralstore.com"],
            "self_disclosed": true
          },
          "email_personal": {
            "value": "napageneral@gmail.com",
            "confidence": "high",
            "evidence": ["LastPass has all my passwords. Napageneral@gmail.com"],
            "self_disclosed": true
          },
          "phone_mobile": {
            "value": "+16508238440",
            "confidence": "high",
            "evidence": ["primary contact identifier"],
            "self_disclosed": false
          }
        },
        "relationships": {
          "spouse": {
            "value": "Jill",
            "confidence": "medium",
            "evidence": ["mentioned in family context"],
            "related_person_ref": "Mom"
          },
          "children": {
            "value": ["Tyler"],
            "confidence": "high",
            "evidence": ["conversation is with son"]
          }
        },
        "professional": {
          "business_owned": {
            "value": ["Napa General Store"],
            "confidence": "high",
            "evidence": ["jim@napageneralstore.com", "owns the store", "napageneral username"]
          },
          "business_role": {
            "value": "Owner",
            "confidence": "high",
            "evidence": ["runs the store", "his business"]
          },
          "profession": {
            "value": "Small Business Owner / Restaurateur",
            "confidence": "medium",
            "evidence": ["inferred from business type"]
          }
        },
        "location_presence": {
          "location_current": {
            "value": "Napa, CA",
            "confidence": "high",
            "evidence": ["napageneralstore.com", "Napa General Store"]
          }
        },
        "digital_identity": {
          "username_unknown": {
            "value": "napageneral",
            "confidence": "high",
            "evidence": ["napageneral is my username"],
            "self_disclosed": true
          }
        },
        "education": {
          "school_previous": {
            "value": "UCLA",
            "confidence": "medium",
            "evidence": ["mentioned attending UCLA"]
          }
        }
      },
      "sensitive_flags": []
    }
  ],
  "new_identity_candidates": [
    {
      "reference": "Janet",
      "known_facts": {
        "given_name": "Janet",
        "relationship_to_primary": "friend/travel companion"
      },
      "note": "New person mentioned, may be worth creating identity node"
    }
  ],
  "unattributed_facts": [
    {
      "fact_type": "phone",
      "fact_value": "+15551234567",
      "shared_by": "Dad",
      "context": "Sent as standalone message with no explanation",
      "possible_attributions": ["Dad''s alternate number", "Third party contact", "Business number"],
      "note": "Cannot determine whose phone number this is without more context"
    }
  ]
}
```

## Conversation to analyze:

{conversation_text}',
    'gemini-2.0-flash',
    strftime('%s', 'now'),
    strftime('%s', 'now')
);
