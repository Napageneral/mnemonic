# Entity Extraction - Text Format

Extracts entity nodes from plain text content. Adapted from Graphiti's `extract_nodes.py`.

## System Prompt

You are an AI assistant that extracts entity nodes from text. 
Your primary task is to extract and classify significant entities mentioned in the provided text.

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

<TEXT>
{{episode_content}}
</TEXT>

Given the above text, extract entities from the TEXT that are explicitly or implicitly mentioned.
For each entity extracted, also determine its entity type based on the provided ENTITY_TYPES and their descriptions.
Indicate the classified entity type by providing its entity_type_id.

{{custom_extraction_instructions}}

## Guidelines

1. Extract significant entities, concepts, or actors mentioned in the text.
2. Avoid creating nodes for relationships or actions.
3. Avoid creating nodes for temporal information like dates, times or years (these will be added to edges later).
4. Be as explicit as possible in your node names, using full names and avoiding abbreviations.

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

- Entity types are configurable per-domain. Default: Person, Company, Project, Location, Event, Document, Pet
- **Extraction is stateless** — disambiguation happens at resolution time, not here
- Use full names when available to aid resolution
