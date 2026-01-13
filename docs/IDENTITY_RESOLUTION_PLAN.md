# Identity Resolution & PII Extraction Plan

## Overview

This document outlines the approach for intelligent identity resolution across communication channels in comms. The core insight: **people share their identifiers in messages all the time**. By extracting ALL PII from message content with LLM intelligence, we can build rich identity graphs that naturally merge when they accumulate enough overlapping facts.

## The Problem

We have contacts in different channels that are the same person but appear as separate entities:

| iMessage | Gmail | The Gap |
|----------|-------|---------|
| "Mom" (+17072268448) | jill@napageneralstore.com | Different name, different channel |
| "Dad" (+16508238440) | jim@napageneralstore.com | Different name, different channel |
| Nic Barradas (+15108629220) | nicbarradas@gmail.com | Same name, but not linked |

Traditional approaches fail:
- **Name matching**: "Mom" ≠ "Jill Brandt"
- **Embedding similarity**: Clusters by topic, not identity
- **Social graph alone**: Can't make the Dad → James Brandt jump
- **Regex extraction**: No context = false positives (whose email is this?)

## The Solution: Identity Graphs + PII Extraction

**One unified mechanism:** Extract ALL PII from every conversation using LLM intelligence that understands context and attribution.

### How It Works

1. **Start with what we know**: Each contact begins as an identity node with their primary identifier (phone, email)

2. **Extract PII from conversations**: Run comprehensive PII extraction on conversation chunks, capturing:
   - ALL identifiers (emails, phones, usernames)
   - Names (full name, nicknames)
   - Relationships
   - Locations, employers, and hundreds of other facts
   - **Crucially**: WHO each fact belongs to (self-disclosed vs mentioned)

3. **Build identity graphs**: Each extraction adds facts to the person's identity graph

4. **Merge on collisions**: When two identity graphs accumulate enough overlapping facts, they merge:
   - **Hard identifiers** (email, phone): Instant merge
   - **Soft identifiers** (full name + employer, nickname + location): Merge when multiple collide

5. **Create new nodes**: Third parties mentioned in conversations become new identity nodes, forming their own graphs until resolved

### Example Flow

```
Day 1: iMessage sync
├── Create node: "Dad" (+16508238440)
└── Extract from Dad's messages:
    ├── email: jim@napageneralstore.com (self-disclosed)
    ├── email: napageneral@gmail.com (self-disclosed)
    ├── full_name: "Jim" / "James" (inferred)
    ├── employer: "Napa General Store" (inferred)
    └── location: "Napa, CA", "Harwich, MA"

Day 2: Gmail sync  
├── Create node: jim@napageneralstore.com
├── Extract from Jim's emails:
│   ├── full_name: "James Brandt" (from signature)
│   ├── phone: (650) 823-8440 (from signature) 
│   ├── employer: "Napa General Store"
│   └── relationships: spouse "Jill"

COLLISION DETECTED:
├── Dad (+16508238440) has email jim@napageneralstore.com
├── jim@napageneralstore.com exists as separate node
└── → MERGE: Dad = Jim Brandt = jim@napageneralstore.com = napageneral@gmail.com
```

## Evidence We Found

Searching actual messages revealed direct self-identification:

```
Dad (+16508238440):
  "napageneral is my username"
  "the recovery email is my jim@napageneralstore.com"
  "LastPass has all my passwords. Napageneral@gmail.com"

Mom (+17072268448):
  "Look in jillbrandt12@gmail.com"

Uncle Scott (+18607294099):
  "send me an email to my office: scott@parsonsfinancial.com"
  "My username: scottjparsons007@gmail.com"
```

**This is direct evidence requiring LLM context to properly attribute.**

---

## The Extraction Pipeline

### Single Mechanism: LLM PII Extraction

Every conversation chunk goes through ONE comprehensive extraction:

```
┌─────────────────────────────────────────────────────────────────────┐
│                    PII EXTRACTION PIPELINE                          │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  INPUT: Conversation chunk (50-100 messages)                       │
│  ├── Channel (iMessage, Gmail, etc.)                               │
│  ├── Primary contact (who the user is talking to)                  │
│  └── User identity (for attribution)                               │
│                                                                     │
│  EXTRACTION (LLM with full PII taxonomy):                          │
│  ├── For primary contact: extract ALL their PII                    │
│  ├── For user: extract any self-disclosed PII                      │
│  ├── For third parties: extract mentioned PII                      │
│  └── Attribute correctly: whose info is this?                      │
│                                                                     │
│  OUTPUT: Structured PII for each person mentioned                   │
│  ├── Core identity (names, DOB, physical description)              │
│  ├── Contact info (emails, phones, addresses)                      │
│  ├── Relationships (family, friends, colleagues)                   │
│  ├── Professional (employer, title, education)                     │
│  ├── Digital identity (usernames, social handles)                  │
│  ├── Location (current, previous, frequent)                        │
│  ├── Lifestyle (hobbies, preferences)                              │
│  └── Sensitive (flagged but stored securely)                       │
│                                                                     │
│  GRAPH UPDATE:                                                      │
│  ├── Add facts to existing identity nodes                          │
│  ├── Create new nodes for unknown third parties                    │
│  └── Check for merge conditions                                    │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### Why LLM-Only (No Regex Fast Pass)

Regex extraction without LLM context leads to false positives:
- "Send this to john@example.com" - Is this John's email or someone else's?
- Phone numbers in messages could be anyone's
- Context matters: "my email" vs "his email" vs "the support email"

The LLM understands:
- **Attribution**: Whose identifier is this?
- **Self-disclosure**: "my email is X" vs "contact X at Y"
- **Relationships**: "send this to mom's email"
- **Context**: Business vs personal, forwarding vs sharing

### Complete PII Taxonomy

See `/Users/tyler/nexus/home/projects/comms/prompts/pii-extraction-v1.prompt.md` for the full taxonomy covering:

1. **Core Identity**: Names, DOB, gender, nationality, physical description
2. **Contact Information**: All emails, phones, addresses
3. **Digital Identity**: Usernames, social handles, websites
4. **Relationships**: Family, professional, social connections
5. **Professional**: Employment, education, certifications
6. **Government IDs**: Passport, DL, SSN (sensitive)
7. **Financial**: Bank accounts, payment handles (sensitive)
8. **Medical**: Health information (sensitive)
9. **Life Events**: Birthdays, anniversaries, milestones
10. **Location**: Current, previous, vacation homes
11. **Lifestyle**: Hobbies, preferences, pets, vehicles

---

## Identity Graph Model

### Core Concept

Each person is an **identity node** with a growing graph of facts:

```
Identity Node: Dad
├── Primary Identifier: +16508238440 (phone)
├── Confidence: 1.0 (known contact)
│
├── Hard Identifiers (instant merge triggers):
│   ├── email: jim@napageneralstore.com (confidence: 0.95)
│   ├── email: napageneral@gmail.com (confidence: 0.95)
│   └── phone: +16508238440 (confidence: 1.0)
│
├── Soft Identifiers (merge when multiple collide):
│   ├── full_name: "James Brandt" (confidence: 0.85)
│   ├── nickname: "Jim" (confidence: 0.90)
│   ├── employer: "Napa General Store" (confidence: 0.90)
│   └── location: "Napa, CA" (confidence: 0.85)
│
├── Enrichment Facts:
│   ├── spouse: "Jill" (confidence: 0.80)
│   ├── children: ["Tyler"] (confidence: 0.95)
│   ├── vacation_home: "Harwich, MA" (confidence: 0.85)
│   ├── username: "napageneral" (confidence: 0.95)
│   └── ...hundreds more possible facts
│
└── Evidence Links:
    └── Each fact links to source message(s)
```

### Merge Conditions

**Automatic Merge (Hard Identifier Match)**:
```
IF identity_a.emails ∩ identity_b.emails ≠ ∅
   OR identity_a.phones ∩ identity_b.phones ≠ ∅
THEN merge(identity_a, identity_b)
```

**Suggested Merge (Soft Identifier Collision)**:
```
IF count(matching_soft_identifiers) >= 3
   AND avg(confidence) >= 0.7
THEN suggest_merge(identity_a, identity_b)

Example:
- Same full_name: "James Brandt"
- Same employer: "Napa General Store"  
- Same location: "Napa, CA"
→ High confidence these are the same person
```

**New Identity Creation**:
```
IF third_party mentioned with sufficient detail
   AND no existing node matches
THEN create_new_node(third_party)

Example: "Meeting up with Jim and Janet"
→ Create node for "Janet" with minimal facts
→ Node grows as more extractions mention her
```

---

## Data Model

### person_facts Table

```sql
CREATE TABLE person_facts (
    id TEXT PRIMARY KEY,
    person_id TEXT NOT NULL REFERENCES persons(id),
    
    -- What fact
    category TEXT NOT NULL,         -- 'core_identity', 'contact', 'relationship', etc.
    fact_type TEXT NOT NULL,        -- 'email_work', 'full_name', 'spouse', etc.
    fact_value TEXT NOT NULL,       -- the actual value
    
    -- Confidence & Source
    confidence REAL DEFAULT 0.5,    -- 0.0-1.0
    source_type TEXT NOT NULL,      -- 'self_disclosed', 'mentioned', 'inferred', 'signature'
    source_channel TEXT,            -- 'iMessage', 'gmail', etc.
    source_event_id TEXT,           -- event where extracted
    evidence TEXT,                  -- quote from message
    
    -- Flags
    is_sensitive INTEGER DEFAULT 0, -- SSN, medical, financial
    is_identifier INTEGER DEFAULT 0, -- can be used for merging
    
    -- Timestamps
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    
    UNIQUE(person_id, category, fact_type, fact_value)
);

CREATE INDEX idx_person_facts_person ON person_facts(person_id);
CREATE INDEX idx_person_facts_type ON person_facts(category, fact_type);
CREATE INDEX idx_person_facts_value ON person_facts(fact_value);
CREATE INDEX idx_person_facts_identifier ON person_facts(is_identifier) WHERE is_identifier = 1;
```

### extraction_runs Table

```sql
CREATE TABLE extraction_runs (
    id TEXT PRIMARY KEY,
    person_id TEXT NOT NULL,
    channel TEXT NOT NULL,
    conversation_id TEXT,           -- if applicable
    event_range_start TEXT,
    event_range_end TEXT,
    message_count INTEGER,
    
    status TEXT DEFAULT 'pending',  -- 'pending', 'complete', 'failed'
    facts_extracted INTEGER DEFAULT 0,
    new_identities_found INTEGER DEFAULT 0,
    
    created_at INTEGER NOT NULL,
    completed_at INTEGER
);
```

### merge_events Table

```sql
CREATE TABLE merge_events (
    id TEXT PRIMARY KEY,
    source_person_id TEXT NOT NULL,
    target_person_id TEXT NOT NULL,
    merge_type TEXT NOT NULL,       -- 'hard_identifier', 'soft_collision', 'manual'
    
    triggering_facts TEXT,          -- JSON array of facts that caused merge
    confidence REAL,
    
    status TEXT DEFAULT 'pending',  -- 'pending', 'accepted', 'rejected'
    
    created_at INTEGER NOT NULL,
    resolved_at INTEGER,
    resolved_by TEXT                -- 'auto' or user action
);
```

---

## CLI Interface

```bash
# Run extraction on top N contacts per channel
comms identify extract [--top N] [--channel iMessage|gmail|all]

# Run extraction on specific person
comms identify extract --person <person_id>

# View extraction status
comms identify extractions [--status pending|complete]

# View person's identity graph
comms person facts <person_id>

# View pending merges
comms identify merges [--status pending|accepted|rejected]

# Accept/reject a merge
comms identify merge <merge_id> --accept|--reject

# Force merge two identities
comms identify merge --force <person_id_1> <person_id_2>

# View full person profile
comms person show <person_id> [--include-evidence]
```

---

## Implementation Plan

### Phase 1: Schema & Infrastructure
- [ ] Add person_facts table
- [ ] Add extraction_runs table  
- [ ] Add merge_events table
- [ ] Create fact insertion/query utilities

### Phase 2: Extraction Pipeline
- [ ] Implement conversation chunking for comms events
- [ ] Integrate PII extraction prompt
- [ ] Build extraction job runner
- [ ] Store facts with evidence links

### Phase 3: Identity Resolution
- [ ] Implement hard identifier matching
- [ ] Implement soft collision detection
- [ ] Build merge suggestion system
- [ ] Create merge execution logic

### Phase 4: CLI & UI
- [ ] Add extraction commands
- [ ] Add merge review commands
- [ ] Add person profile commands
- [ ] Build fact browsing interface

### Phase 5: Bidirectional Extraction
- [ ] Run on iMessage conversations
- [ ] Run on Gmail threads
- [ ] Run on AIX sessions
- [ ] Cross-reference all channels

---

## Cost Analysis

| Operation | LLM Calls | Cost Estimate |
|-----------|-----------|---------------|
| Extract top 100 iMessage contacts | ~200 | $4-8 |
| Extract top 100 Gmail contacts | ~200 | $4-8 |
| Extract AIX sessions | ~50 | $1-2 |
| **Initial full extraction** | ~450 | **$10-18** |

Incremental: Only new conversations, ~$0.50-1/week for active user.

---

## Future Enhancements

1. **Continuous extraction**: Run on new messages in real-time
2. **Confidence decay**: Facts lose confidence if not re-confirmed over time
3. **Conflict resolution**: Handle contradictory facts (two different birthdays)
4. **Privacy controls**: Let users mark facts as "don't extract" or "private"
5. **Export**: Export contact profiles as vCards with all facts
6. **Search**: "Who works at Google?" → query person_facts

---

## Related Documents

- **PII Extraction Prompt**: `/Users/tyler/nexus/home/projects/comms/prompts/pii-extraction-v1.prompt.md`
- **Eve Migration Analysis**: `/Users/tyler/nexus/home/projects/comms/docs/EVE_COMMS_MIGRATION_ANALYSIS.md`
