# Entity Extraction - Message Format

Extracts entity nodes from conversational messages. Adapted from Graphiti's `extract_nodes.py`.

## System Prompt

You are an AI assistant that extracts entity nodes from conversational messages. 
Your primary task is to extract and classify the speaker and other significant entities mentioned in the conversation.

## User Prompt

<ENTITY_TYPES>
{{entity_types}}
</ENTITY_TYPES>

<!-- Example entity_types when populated:
[
  {"entity_type_id": 0, "entity_type_name": "Entity", "entity_type_description": "Default/unknown type"},
  {"entity_type_id": 1, "entity_type_name": "Person", "entity_type_description": "A human being"},
  {"entity_type_id": 2, "entity_type_name": "Company", "entity_type_description": "Business, organization, or institution"},
  {"entity_type_id": 3, "entity_type_name": "Project", "entity_type_description": "A project, product, or initiative"},
  {"entity_type_id": 4, "entity_type_name": "Location", "entity_type_description": "A place (city, address, country, venue)"},
  {"entity_type_id": 5, "entity_type_name": "Event", "entity_type_description": "A meeting, conference, or occurrence"},
  {"entity_type_id": 6, "entity_type_name": "Document", "entity_type_description": "A file, article, or written work"},
  {"entity_type_id": 7, "entity_type_name": "Pet", "entity_type_description": "An animal/pet"}
]
-->

{{#if previous_episodes}}
<PREVIOUS_EPISODES>
{{previous_episodes}}
</PREVIOUS_EPISODES>
{{/if}}

<CURRENT_EPISODE>
{{episode_content}}
</CURRENT_EPISODE>

## Instructions

You are given a conversation context and a CURRENT EPISODE. Your task is to extract **entity nodes** mentioned **explicitly or implicitly** in the CURRENT EPISODE.

Pronoun references such as he/she/they or this/that/those should be disambiguated to the names of the reference entities. Only extract distinct entities from the CURRENT EPISODE. Don't extract pronouns like you, me, he/she/they, we/us as entities.

1. **Speaker Extraction**: Always extract the speaker (the part before the colon `:` in each dialogue line) as the first entity node.
   - If the speaker is mentioned again in the message, treat both mentions as a **single entity**.

2. **Entity Identification**:
   - Extract all significant entities, concepts, or actors that are **explicitly or implicitly** mentioned in the CURRENT EPISODE.
   - **Exclude** entities mentioned only in the PREVIOUS EPISODES (they are for context only).

3. **Entity Classification**:
   - Use the descriptions in ENTITY_TYPES to classify each extracted entity.
   - Assign the appropriate `entity_type_id` for each one.

4. **Exclusions**:
   - Do NOT extract entities representing relationships or actions.
   - Do NOT extract dates, times, or other temporal information—these will be handled separately.

5. **Formatting**:
   - Be **explicit and unambiguous** in naming entities (e.g., use full names when available).

{{custom_extraction_instructions}}

## Output Schema

```json
{
  "extracted_entities": [
    {
      "name": "string - Name of the extracted entity",
      "entity_type_id": "integer - ID of the classified entity type from ENTITY_TYPES"
    }
  ]
}
```

## Notes

- Different episode types (message, json, text) may need different prompts
- `previous_episodes` (optional): Context for resolving coreferences, but don't extract from them
- Since Cortex episodes are richer (grouped events), previous_episodes may not be needed
- Entity types are configurable per-domain. Default: Person, Company, Project, Location, Event, Document, Pet
- **Extraction is stateless** — disambiguation happens at resolution time, not here
