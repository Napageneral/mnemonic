# Graphiti-Inspired Prompts

These prompts are adapted from [Graphiti](https://github.com/getzep/graphiti), Zep's open-source temporal knowledge graph framework.

## Pipeline Sequence

The Graphiti extraction pipeline runs in this order:

```
1. EXTRACT ENTITIES (extract-entities-*.prompt.md)
   └─ Identify people, places, things from episode content
   
2. REFLEXION - ENTITIES (reflexion-entities.prompt.md)
   └─ Self-check: did we miss any entities?

3. RESOLVE ENTITIES (resolve-entities.prompt.md)
   └─ Match extracted entities to existing nodes (deduplication)
   
4. EXTRACT RELATIONSHIPS (extract-relationships.prompt.md)
   └─ Identify facts/edges between resolved entities
   
5. EXTRACT DATES (extract-dates.prompt.md)
   └─ Extract temporal bounds for each relationship
   
6. REFLEXION - RELATIONSHIPS (reflexion-relationships.prompt.md)
   └─ Self-check: did we miss any facts?

7. RESOLVE EDGES (resolve-edges.prompt.md)
   └─ Match extracted edges to existing edges (deduplication)
   
8. DETECT CONTRADICTIONS (detect-contradictions.prompt.md)
   └─ Find existing facts that new facts contradict
   └─ Mark contradicted edges with invalid_at timestamp

9. SUMMARIZE ENTITY (summarize-entity.prompt.md)
   └─ Update entity summaries with new information
```

## Key Concepts

### Episodes
Temporal units of content (messages, documents, events). Each episode:
- Has a `valid_at` timestamp (when it occurred)
- Has a `created_at` timestamp (when we ingested it)
- Can be typed (message, json, text)

### Entities (Nodes)
Things extracted from episodes:
- People, companies, projects, files, concepts
- Have types from a defined ontology
- Can be deduplicated against existing entities
- Accumulate summaries over time

### Relationships (Edges)
Facts connecting two entities:
- Have a type (WORKS_AT, KNOWS, CREATED)
- Have a natural language `fact` description
- Have temporal bounds (valid_at, invalid_at)
- Can be invalidated when contradicted

### Bi-Temporal Model
Every fact tracks two time dimensions:
- **Valid time**: when the fact is true in reality
- **Transaction time**: when we learned about it

This enables queries like:
- "What did we believe on January 1st?" (transaction time)
- "What was true on January 1st?" (valid time)

## Customization

### Entity Types
Define your domain's entity types:
```json
{
  "entity_types": [
    {"id": 0, "name": "Person", "description": "A human being"},
    {"id": 1, "name": "Company", "description": "An organization or business"},
    {"id": 2, "name": "Project", "description": "A work effort or initiative"},
    {"id": 3, "name": "File", "description": "A file or document"}
  ]
}
```

### Relationship Types
Define common relationship patterns:
```json
{
  "edge_types": [
    {"name": "WORKS_AT", "signature": "Person -> Company"},
    {"name": "KNOWS", "signature": "Person -> Person"},
    {"name": "OWNS", "signature": "Person -> Project"},
    {"name": "CREATED", "signature": "Person -> File"}
  ]
}
```

### Custom Instructions
Each prompt accepts `{{custom_extraction_instructions}}` for domain-specific guidance:
```
Focus on extracting:
- Trading system components and their relationships
- Client names and project associations
- Technical decisions and their rationale
```

## Integration with Cortex

These prompts can be used in Cortex's extraction pipeline:

1. **Episode = Segment**: Cortex segments become Graphiti episodes
2. **Facets + Relationships**: Extract both simple facets AND relationship triples
3. **Bi-temporal tracking**: Add valid_at/invalid_at to facets table
4. **Dedup inline**: Resolve during extraction, not in batch

## References

- [Graphiti GitHub](https://github.com/getzep/graphiti)
- [Graphiti Docs](https://help.getzep.com/graphiti)
- [Zep Paper: Temporal Knowledge Graph Architecture](https://arxiv.org/abs/2501.13956)
