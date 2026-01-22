# Unified Entity & Relationship Extraction

Extracts entities and relationships from any content type. This is the primary extraction prompt for the Cortex memory system.

## System Prompt

You are an AI assistant that extracts knowledge graph data from text.

Your task is to identify:
1. **Entities**: People, organizations, places, dates, concepts, technologies, activities, and other meaningful things
2. **Relationships**: Facts connecting entities (triples: source → relation → target)

Everything meaningful should be captured as entities connected by relationships. This enables powerful queries like "Who likes hiking?" or "What has Claude explained to me?"

## User Prompt

<ENTITY_TYPES>
{{entity_types}}
</ENTITY_TYPES>

<!-- Default entity types:
[
  {"id": 0, "name": "Entity", "description": "Default/unknown type"},
  {"id": 1, "name": "Person", "description": "A human being"},
  {"id": 2, "name": "Agent", "description": "An AI assistant or bot (Claude, GPT, etc.)"},
  {"id": 3, "name": "Company", "description": "Business, organization, or institution"},
  {"id": 4, "name": "Project", "description": "A project, product, or codebase"},
  {"id": 5, "name": "Location", "description": "A place (city, address, country, venue)"},
  {"id": 6, "name": "Date", "description": "A specific date or time point (e.g., 1990-05-15, January 2024)"},
  {"id": 7, "name": "Event", "description": "A meeting, conference, or occurrence"},
  {"id": 8, "name": "Document", "description": "A file, article, or written work"},
  {"id": 9, "name": "Concept", "description": "An idea, topic, or abstract notion"},
  {"id": 10, "name": "Technology", "description": "A tool, programming language, or framework"},
  {"id": 11, "name": "Activity", "description": "A hobby, sport, or activity"},
  {"id": 12, "name": "Profession", "description": "A job role or career"},
  {"id": 13, "name": "Pet", "description": "An animal companion"}
]
-->

<REFERENCE_TIME>
{{reference_time}}
</REFERENCE_TIME>

{{#if previous_episodes}}
<PREVIOUS_EPISODES>
{{previous_episodes}}
</PREVIOUS_EPISODES>
{{/if}}

<CURRENT_EPISODE>
{{episode_content}}
</CURRENT_EPISODE>

## Instructions

### Entity Extraction

Extract all significant entities mentioned in the CURRENT_EPISODE:

1. **People**: Extract by name. If only a role is mentioned ("my dad", "the CEO"), use a descriptive name.
2. **AI Agents**: Claude, GPT, Copilot, etc. — these are separate from Person.
3. **Organizations**: Companies, schools, institutions.
4. **Projects**: Software, products, initiatives being discussed.
5. **Locations**: Cities, addresses, countries, venues.
6. **Dates**: Specific dates mentioned. Normalize to ISO format when possible (YYYY-MM-DD). Use REFERENCE_TIME to resolve relative dates ("yesterday", "next week").
7. **Events**: Meetings, conferences, occurrences with names.
8. **Documents**: Files, articles, specs being referenced.
9. **Concepts**: Abstract ideas, topics, theories being discussed.
10. **Technologies**: Languages, frameworks, tools mentioned.
11. **Activities**: Hobbies, sports, interests mentioned.
12. **Professions**: Job roles, careers mentioned.

**Rules:**
- Use the most complete name available (full names over nicknames)
- Resolve pronouns to their referent when clear
- Don't extract generic words that aren't specific entities
- Do NOT extract temporal information as entities UNLESS it's a specific date

### Relationship Extraction

Extract relationships (facts) connecting entities:

1. Each relationship is a triple: (source_entity) --RELATION_TYPE--> (target_entity)
2. Use SCREAMING_SNAKE_CASE for relation_type (e.g., WORKS_AT, LIKES, BORN_ON)
3. Write a natural language `fact` that captures the relationship
4. Include `source_type`: 'self_disclosed' (speaker about themselves), 'mentioned' (about others), 'inferred'
5. For temporal relationships, include `valid_at` and/or `invalid_at` if known

**Common relationship types:**
- Identity: HAS_EMAIL, HAS_PHONE, HAS_HANDLE, ALSO_KNOWN_AS
- Personal: IS_A, LIKES, DISLIKES, BORN_ON, BORN_IN, LIVES_IN, HAS_DIET
- Professional: WORKS_AT, OWNS, FOUNDED, ATTENDED, HAS_ROLE
- Social: KNOWS, FRIEND_OF, SPOUSE_OF, PARENT_OF, COLLEAGUE_OF
- AI: EXPLAINED, SUGGESTED, ASKED_ABOUT, DISCUSSED, IMPLEMENTED
- Projects: USES, DEPENDS_ON, CREATED, CONTRIBUTED_TO
- Temporal: OCCURRED_ON, SCHEDULED_FOR, STARTED_ON, ENDED_ON

{{custom_extraction_instructions}}

## Output Schema

```json
{
  "extracted_entities": [
    {
      "id": "integer - Temporary ID for reference in relationships (0, 1, 2, ...)",
      "name": "string - Name of the entity",
      "entity_type_id": "integer - ID from ENTITY_TYPES"
    }
  ],
  "extracted_relationships": [
    {
      "source_entity_id": "integer - ID of source entity from extracted_entities",
      "relation_type": "string - SCREAMING_SNAKE_CASE relation type",
      "target_entity_id": "integer - ID of target entity from extracted_entities",
      "fact": "string - Natural language description of the relationship",
      "source_type": "string - 'self_disclosed', 'mentioned', or 'inferred'",
      "valid_at": "string (optional) - ISO date when relationship became true",
      "invalid_at": "string (optional) - ISO date when relationship stopped being true"
    }
  ]
}
```

## Examples

### Example 1: Personal Information

**Input:**
```
Tyler: My birthday is May 15th, 1990. I've been working at Anthropic since January.
Casey: Oh nice! I thought you were still at Intent Systems.
```

**Output:**
```json
{
  "extracted_entities": [
    {"id": 0, "name": "Tyler", "entity_type_id": 1},
    {"id": 1, "name": "1990-05-15", "entity_type_id": 6},
    {"id": 2, "name": "Anthropic", "entity_type_id": 3},
    {"id": 3, "name": "2024-01", "entity_type_id": 6},
    {"id": 4, "name": "Casey", "entity_type_id": 1},
    {"id": 5, "name": "Intent Systems", "entity_type_id": 3}
  ],
  "extracted_relationships": [
    {
      "source_entity_id": 0,
      "relation_type": "BORN_ON",
      "target_entity_id": 1,
      "fact": "Tyler was born on May 15th, 1990",
      "source_type": "self_disclosed"
    },
    {
      "source_entity_id": 0,
      "relation_type": "WORKS_AT",
      "target_entity_id": 2,
      "fact": "Tyler works at Anthropic",
      "source_type": "self_disclosed",
      "valid_at": "2024-01"
    },
    {
      "source_entity_id": 0,
      "relation_type": "WORKS_AT",
      "target_entity_id": 5,
      "fact": "Tyler previously worked at Intent Systems",
      "source_type": "mentioned",
      "invalid_at": "2024-01"
    }
  ]
}
```

### Example 2: AI Conversation

**Input:**
```
Tyler: Can you explain how bi-temporal models work?
Claude: A bi-temporal model tracks two time dimensions: when something was true in reality (valid time) and when the system learned about it (transaction time). This is useful for maintaining accurate historical records.
Tyler: That makes sense for the memory system we're building.
```

**Output:**
```json
{
  "extracted_entities": [
    {"id": 0, "name": "Tyler", "entity_type_id": 1},
    {"id": 1, "name": "Claude", "entity_type_id": 2},
    {"id": 2, "name": "Bi-Temporal Model", "entity_type_id": 9},
    {"id": 3, "name": "Memory System", "entity_type_id": 4}
  ],
  "extracted_relationships": [
    {
      "source_entity_id": 0,
      "relation_type": "ASKED_ABOUT",
      "target_entity_id": 2,
      "fact": "Tyler asked about how bi-temporal models work",
      "source_type": "self_disclosed"
    },
    {
      "source_entity_id": 1,
      "relation_type": "EXPLAINED",
      "target_entity_id": 2,
      "fact": "Claude explained that bi-temporal models track valid time and transaction time",
      "source_type": "mentioned"
    },
    {
      "source_entity_id": 3,
      "relation_type": "USES",
      "target_entity_id": 2,
      "fact": "The memory system Tyler is building uses bi-temporal models",
      "source_type": "inferred"
    }
  ]
}
```

### Example 3: Interests and Activities

**Input:**
```
Ricky loves hiking and chess. He mentioned that his girlfriend Hannah also enjoys hiking but prefers software engineers over hikers.
```

**Output:**
```json
{
  "extracted_entities": [
    {"id": 0, "name": "Ricky", "entity_type_id": 1},
    {"id": 1, "name": "Hiking", "entity_type_id": 11},
    {"id": 2, "name": "Chess", "entity_type_id": 11},
    {"id": 3, "name": "Hannah", "entity_type_id": 1},
    {"id": 4, "name": "Software Engineer", "entity_type_id": 12}
  ],
  "extracted_relationships": [
    {
      "source_entity_id": 0,
      "relation_type": "LIKES",
      "target_entity_id": 1,
      "fact": "Ricky loves hiking",
      "source_type": "mentioned"
    },
    {
      "source_entity_id": 0,
      "relation_type": "LIKES",
      "target_entity_id": 2,
      "fact": "Ricky loves chess",
      "source_type": "mentioned"
    },
    {
      "source_entity_id": 0,
      "relation_type": "DATING",
      "target_entity_id": 3,
      "fact": "Ricky is dating Hannah",
      "source_type": "mentioned"
    },
    {
      "source_entity_id": 3,
      "relation_type": "LIKES",
      "target_entity_id": 1,
      "fact": "Hannah enjoys hiking",
      "source_type": "mentioned"
    },
    {
      "source_entity_id": 3,
      "relation_type": "PREFERS",
      "target_entity_id": 4,
      "fact": "Hannah prefers software engineers",
      "source_type": "mentioned"
    }
  ]
}
```

## Notes

- **Extraction is stateless** — disambiguation happens at resolution time
- Entity IDs are temporary (0, 1, 2...) for this extraction only; resolution assigns real UUIDs
- The `fact` field preserves the natural language evidence
- `source_type` helps with confidence scoring during resolution
- Dates should be extracted as Date entities so they can be queried ("What happened on my birthday?")
- Concepts, Activities, and Professions become entities so relationships can be traversed both ways
