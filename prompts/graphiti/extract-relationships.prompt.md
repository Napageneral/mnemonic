# Relationship Extraction

Extracts fact triples (relationships between entities) from episodes. Adapted from Graphiti's `extract_edges.py`.

## System Prompt

You are an expert fact extractor that extracts fact triples from text.

1. Extracted fact triples should also be extracted with relevant date information.
2. Treat the REFERENCE_TIME as the time the CURRENT EPISODE was sent. All temporal information should be extracted relative to this time.

## User Prompt

<FACT_TYPES>
{{edge_types}}
</FACT_TYPES>

<PREVIOUS_EPISODES>
{{previous_episodes}}
</PREVIOUS_EPISODES>

<CURRENT_EPISODE>
{{episode_content}}
</CURRENT_EPISODE>

<ENTITIES>
{{entities}}
</ENTITIES>

<REFERENCE_TIME>
{{reference_time}}  # ISO 8601 (UTC); used to resolve relative time mentions
</REFERENCE_TIME>

## Task

Extract all factual relationships between the given ENTITIES based on the CURRENT EPISODE.

Only extract facts that:
- involve two DISTINCT ENTITIES from the ENTITIES list
- are clearly stated or unambiguously implied in the CURRENT EPISODE
- can be represented as edges in a knowledge graph
- include entity names rather than pronouns whenever possible

The FACT_TYPES provide a list of the most important types of facts. Make sure to extract facts of these types. However, FACT_TYPES are not exhaustive - extract all facts from the episode even if they do not fit into one of the FACT_TYPES.

You may use information from the PREVIOUS EPISODES only to disambiguate references or support continuity.

{{custom_extraction_instructions}}

## Extraction Rules

1. **Entity ID Validation**: `source_entity_id` and `target_entity_id` must use only the `id` values from the ENTITIES list.
   - **CRITICAL**: Using IDs not in the list will cause the edge to be rejected
2. Each fact must involve two **distinct** entities.
3. Use a SCREAMING_SNAKE_CASE string as the `relation_type` (e.g., FOUNDED, WORKS_AT, KNOWS).
4. Do not emit duplicate or semantically redundant facts.
5. The `fact` should closely paraphrase the original source sentence(s). Do not verbatim quote.
6. Use `REFERENCE_TIME` to resolve vague or relative temporal expressions (e.g., "last week").
7. Do **not** hallucinate or infer temporal bounds from unrelated events.

## Datetime Rules

- Use ISO 8601 with "Z" suffix (UTC) (e.g., 2025-04-30T00:00:00Z).
- If the fact is ongoing (present tense), set `valid_at` to REFERENCE_TIME.
- If a change/termination is expressed, set `invalid_at` to the relevant timestamp.
- Leave both fields `null` if no explicit or resolvable time is stated.
- If only a date is mentioned (no time), assume 00:00:00.
- If only a year is mentioned, use January 1st at 00:00:00.

## Output Schema

```json
{
  "edges": [
    {
      "relation_type": "string - FACT_PREDICATE_IN_SCREAMING_SNAKE_CASE",
      "source_entity_id": "integer - ID of the source entity from ENTITIES list",
      "target_entity_id": "integer - ID of the target entity from ENTITIES list",
      "fact": "string - Natural language description of the relationship",
      "valid_at": "string|null - ISO 8601 datetime when relationship became true",
      "invalid_at": "string|null - ISO 8601 datetime when relationship stopped being true"
    }
  ]
}
```

## Example

Given entities: [{id: 0, name: "Tyler"}, {id: 1, name: "Intent Systems"}]
Episode: "Tyler has been working at Intent Systems since 2024"
Reference time: 2026-01-21T10:00:00Z

Output:
```json
{
  "edges": [
    {
      "relation_type": "WORKS_AT",
      "source_entity_id": 0,
      "target_entity_id": 1,
      "fact": "Tyler has been working at Intent Systems since 2024",
      "valid_at": "2024-01-01T00:00:00Z",
      "invalid_at": null
    }
  ]
}
```
