# Agents Ledger Schema Documentation

This document explains the full-fidelity AI session data captured in Mnemonic's Agents Ledger. This data flows from Cursor → AIX → Mnemonic.

## Overview

The Agents Ledger captures **everything** about AI sessions for:
- **Smart Forking**: Resume from any point in a conversation
- **Deep Analysis**: Understand agent behavior, tool usage patterns
- **Replay & Debugging**: See exactly what happened in a session

## Data Flow

```
Cursor IDE → ~/.cursor/ logs → AIX (aix sync) → Mnemonic (aix-agents adapter)
```

---

## Tables

### 1. `agent_sessions` - The Container

A session represents one continuous conversation with an AI agent. Sessions can be **main** (human-initiated) or **subagents** (spawned by Task tool).

#### Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | TEXT PK | UUID for main sessions, `task-{tool_call_id}` for subagents |
| `source` | TEXT | Where it came from: `cursor`, `codex`, `nexus`, `clawdbot` |
| `model` | TEXT | Model used: `claude-4.5-opus-high-thinking`, `gpt-5.2-codex-xhigh`, `composer-1` |
| `project` | TEXT | Project name from workspace (e.g., `nexus`, `ChatStats`) |
| `created_at` | INTEGER | Unix timestamp in milliseconds |
| `message_count` | INTEGER | Total messages in session |

#### Subagent Fields

| Field | Type | Description |
|-------|------|-------------|
| `parent_session_id` | TEXT | ID of the session that spawned this subagent |
| `parent_message_id` | TEXT | Message ID where Task tool was called |
| `tool_call_id` | TEXT | The Task tool call ID (e.g., `toolu_bdrk_01BFM9Va...`) |
| `task_description` | TEXT | What the subagent was asked to do |
| `task_status` | TEXT | `completed`, `cancelled`, `failed` |
| `is_subagent` | INTEGER | 1 for subagents, 0 for main sessions |

#### Context Fields

| Field | Type | Description |
|-------|------|-------------|
| `context_token_limit` | INTEGER | Max context window (e.g., 176000 for Opus) |
| `context_tokens_used` | INTEGER | How much context was used |
| `is_agentic` | INTEGER | 1 if agent mode (tools enabled), 0 for chat-only |
| `force_mode` | TEXT | `edit`, `agent`, `ask`, `plan` - the mode forced by UI |
| `workspace_path` | TEXT | Absolute path to workspace root |

#### Examples

**Main Session (human starts conversation):**
```json
{
  "id": "a3730dd7-5c30-4081-9876-3c28e4505cc9",
  "source": "cursor",
  "model": "claude-4.5-opus-high-thinking",
  "project": "nexus",
  "message_count": 56,
  "is_subagent": 0,
  "is_agentic": 1,
  "context_token_limit": 176000,
  "context_tokens_used": 38191,
  "force_mode": "edit"
}
```

**Subagent (spawned by Task tool):**
```json
{
  "id": "task-toolu_bdrk_01BFM9VaztN9grH5e4zFhQyZ",
  "source": "cursor",
  "model": "composer-1",
  "message_count": 16,
  "parent_session_id": "872520ab-a30e-4d85-8f7e-9f925992cebe",
  "tool_call_id": "toolu_bdrk_01BFM9VaztN9grH5e4zFhQyZ",
  "task_description": "Update ONBOARDING.md to align",
  "task_status": "completed",
  "is_subagent": 1,
  "is_agentic": 1
}
```

---

### 2. `agent_messages` - The Content

Every message exchanged in a session: user input, assistant responses, system prompts.

#### Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | TEXT PK | UUID of the message |
| `session_id` | TEXT FK | Which session this belongs to |
| `role` | TEXT | `user`, `assistant`, `system` |
| `content` | TEXT | The actual message text (can be large) |
| `sequence` | INTEGER | Order within session (0-indexed) |
| `timestamp` | INTEGER | Unix timestamp in milliseconds |

#### Context Fields

| Field | Type | Description |
|-------|------|-------------|
| `checkpoint_id` | TEXT | Links to Cursor's checkpoint system for forking |
| `is_agentic` | INTEGER | 1 if this message was in agentic mode |
| `is_plan_execution` | INTEGER | 1 if executing a plan step |
| `context_json` | TEXT | JSON blob of context attached to message |
| `cursor_rules_json` | TEXT | Active .cursorrules at time of message |
| `metadata_json` | TEXT | **Rich metadata** (see below) |

#### The `metadata_json` Field

This is the treasure trove. It contains everything Cursor tracks about a message:

```json
{
  "_v": 3,                           // Schema version
  "type": 2,                         // Message type enum
  "bubbleId": "uuid",                // Links back to Cursor UI
  "createdAt": "2026-01-28T16:09:18.745Z",
  
  // Context that was attached
  "codebaseContextChunks": [...],    // Code snippets in context
  "attachedCodeChunks": [...],       // Explicitly attached code
  "attachedFolders": [...],          // Folders added to context
  "relevantFiles": [...],            // Files Cursor deemed relevant
  
  // Git context
  "commits": [...],                  // Recent commits
  "pullRequests": [...],             // Related PRs
  "gitDiffs": [...],                 // Diffs in context
  
  // Agent behavior
  "toolResults": [...],              // Results from tool calls
  "todos": [...],                    // TodoWrite state
  "cursorRules": [...],              // Active rules
  
  // UI state
  "lints": [...],                    // Linter errors shown
  "interpreterResults": [...],       // Code execution results
  "images": [...],                   // Attached images
  
  // Token tracking
  "tokenCount": {
    "inputTokens": 0,
    "outputTokens": 0
  },
  
  // Mode info
  "isAgentic": false,
  "unifiedMode": 2,                  // Mode enum
  "conversationState": "~"           // Serialized state
}
```

#### Examples

**User Message:**
```json
{
  "id": "342e1403-90ec-43b4-8a0e-037385b88ea6",
  "session_id": "a3730dd7-5c30-4081-9876-3c28e4505cc9",
  "role": "user",
  "content": "hmm this is fine, but I dont want you updating this file yourself anymore...",
  "sequence": 50,
  "timestamp": 1769616537000,
  "is_agentic": 0,
  "checkpoint_id": "c0264a7f-0573-438f-b89f-24e07fdd1233"
}
```

**Assistant Message with Tool Call:**
```json
{
  "id": "4af766f6-a2a9-45cd-a4b2-894cc72d8799",
  "role": "assistant",
  "content": "",  // Empty! Response was via tool call
  "metadata_json": {
    "toolFormerData": {
      "tool": 15,
      "name": "run_terminal_command_v2",
      "status": "completed",
      "params": "{\"command\":\"sleep 15\"}",
      "result": "{\"rejected\":false}"
    }
  }
}
```

---

### 3. `agent_turns` - The Exchange

A turn is one complete query→response exchange. Turns track the **conversational structure** for smart forking.

#### Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | TEXT PK | UUID of the turn (usually same as response message ID) |
| `session_id` | TEXT FK | Which session |
| `parent_turn_id` | TEXT | Previous turn in conversation (for branching) |
| `query_message_ids` | TEXT | JSON array of user message IDs that form the query |
| `response_message_id` | TEXT | The assistant message ID that is the response |
| `model` | TEXT | Model used for this specific turn |
| `token_count` | INTEGER | Tokens used in this turn |
| `timestamp` | INTEGER | When this turn completed |
| `has_children` | INTEGER | 1 if there are turns that branch from this one |
| `tool_call_count` | INTEGER | How many tool calls in this turn's response |

#### Why Turns Matter for Forking

Turns represent the **forkable checkpoints**. Each turn has:
- A `parent_turn_id` pointing to the previous state
- A `query_message_ids` array (the human's input)
- A `response_message_id` (the agent's output)

To fork from a turn, you replay the session up to that turn's parent, then inject a new query.

#### Examples

**Simple Turn (1 tool call):**
```json
{
  "id": "11f91921-e7e6-4509-afb9-746653c8e639",
  "session_id": "a3730dd7-5c30-4081-9876-3c28e4505cc9",
  "parent_turn_id": "54455e57-4d63-4220-9c9f-106e4cadfc1d",
  "query_message_ids": "[\"342e1403-90ec-43b4-8a0e-037385b88ea6\"]",
  "response_message_id": "11f91921-e7e6-4509-afb9-746653c8e639",
  "model": "claude-4.5-opus-high-thinking",
  "tool_call_count": 1
}
```

**Heavy Turn (597 tool calls!):**
```json
{
  "id": "0751cf1f-b680-457d-a5db-568984466ce1",
  "session_id": "53a4d6c2-9752-41f9-b466-1d9d1ae08dee",
  "parent_turn_id": "1b141bbf-ee04-4fd5-a8a9-f7069b866a30",
  "query_message_ids": "[\"c2b62701-cd72-41dc-a189-3bbd3d1abfea\"]",
  "response_message_id": "0751cf1f-b680-457d-a5db-568984466ce1",
  "model": "gpt-5.2-codex-xhigh",
  "tool_call_count": 597
}
```

---

### 4. `agent_tool_calls` - The Actions

Every tool the agent invoked: file reads, edits, terminal commands, searches, todos.

#### Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | TEXT PK | Tool call ID (e.g., `toolu_01Abc...`) |
| `message_id` | TEXT FK | Which message triggered this tool |
| `session_id` | TEXT FK | Which session |
| `tool_name` | TEXT | Tool name (see list below) |
| `tool_number` | INTEGER | Order within the message (0-indexed) |
| `params_json` | TEXT | Input parameters as JSON |
| `result_json` | TEXT | Output/result as JSON |
| `status` | TEXT | `completed`, `cancelled`, `failed`, `rejected` |
| `child_session_id` | TEXT | For Task tool: the subagent session spawned |
| `started_at` | INTEGER | When tool started |
| `completed_at` | INTEGER | When tool finished |

#### Common Tool Names

| Tool | Description | Params |
|------|-------------|--------|
| `read_file` | Read file contents | `{relativeWorkspacePath, maxLines}` |
| `read_file_v2` | Enhanced file reader | `{path, offset, limit}` |
| `edit_file` | Apply edits to file | `{relativeWorkspacePath}` |
| `edit_file_v2` | Enhanced editor | `{path, old_string, new_string}` |
| `search_replace` | Find and replace | `{path, search, replace}` |
| `run_terminal_command_v2` | Execute shell command | `{command, cwd}` |
| `run_terminal_cmd` | Legacy terminal | `{command}` |
| `grep` | Search codebase | `{pattern, path}` |
| `ripgrep_raw_search` | Raw ripgrep | `{query, directory}` |
| `glob_file_search` | Find files by pattern | `{pattern}` |
| `todo_write` | Manage task list | `{todos: [...]}` |
| `read_lints` | Get linter errors | `{paths}` |
| `apply_patch` | Apply unified diff | `{patch}` |
| `write` | Create new file | `{path, contents}` |

#### Examples

**File Read:**
```json
{
  "id": "tool_b277fa6a-e88c-4d15-b54d-c89c00fa96c",
  "tool_name": "read_file",
  "status": "completed",
  "params_json": {
    "relativeWorkspacePath": "app/electron/ipc/webserver/auth-utils.ts",
    "readEntireFile": true,
    "maxLines": 1500
  },
  "result_length": 3710
}
```

**Terminal Command:**
```json
{
  "id": "toolu_015gV4YYZ3qxn3RHSEFA2Biq",
  "tool_name": "run_terminal_command_v2",
  "status": "completed",
  "params_json": {
    "command": "mkdir -p /path/to/dirs",
    "cwd": "",
    "parsingResult": {
      "executableCommands": [
        {"name": "mkdir", "args": [...]}
      ]
    }
  },
  "result_json": {"rejected": false}
}
```

**Todo Management:**
```json
{
  "id": "toolu_01XYZ",
  "tool_name": "todo_write",
  "status": "completed",
  "params_json": {
    "todos": [
      {
        "id": "rename_ca_repo",
        "content": "Rename ca_repo.py to conversation_analysis_repository.py",
        "status": "in_progress"
      }
    ]
  }
}
```

---

## Relationships

```
agent_sessions (1) ─────────────────┬──── (N) agent_messages
       │                            │
       │ (subagent)                 └──── (N) agent_turns
       │                                        │
       └── parent_session_id ◄──────────────────┘ (via session_id)
                                                │
agent_tool_calls (N) ───────────────────────────┘ (via session_id, message_id)
       │
       └── child_session_id ──► agent_sessions (subagent)
```

## Query Patterns

### Get full conversation history:
```sql
SELECT * FROM agent_messages
WHERE session_id = ?
ORDER BY sequence;
```

### Get turn tree for forking:
```sql
WITH RECURSIVE turn_tree AS (
  SELECT * FROM agent_turns WHERE id = ?  -- start from turn
  UNION ALL
  SELECT t.* FROM agent_turns t
  JOIN turn_tree tt ON t.id = tt.parent_turn_id
)
SELECT * FROM turn_tree ORDER BY timestamp;
```

### Get all subagents for a session:
```sql
SELECT * FROM agent_sessions
WHERE parent_session_id = ?;
```

### Get tool usage stats:
```sql
SELECT tool_name, COUNT(*) as uses, 
       SUM(CASE WHEN status='completed' THEN 1 ELSE 0 END) as success
FROM agent_tool_calls
WHERE session_id = ?
GROUP BY tool_name
ORDER BY uses DESC;
```

---

## Events Ledger vs Agents Ledger

| Aspect | Events Ledger | Agents Ledger |
|--------|---------------|---------------|
| Purpose | Human-readable memory | Full fidelity replay |
| Granularity | Turn level | Message + tool level |
| Content | Stripped (no tool XML) | Raw (everything) |
| Use case | "What did we discuss?" | "What exactly happened?" |
| Tables | `events`, `threads` | `agent_*` tables |
