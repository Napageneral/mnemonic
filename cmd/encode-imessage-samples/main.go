package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Napageneral/cortex/internal/config"
)

type eventRow struct {
	ID           string
	Timestamp    time.Time
	Sender       string
	Content      string
	ContentTypes string
	MetadataJSON sql.NullString
	ReplyTo      sql.NullString
	Members      sql.NullString
	Attachments  sql.NullString
}

func main() {
	dbPath, err := getCortexDBPath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving cortex.db: %v\n", err)
		os.Exit(2)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening cortex.db: %v\n", err)
		os.Exit(2)
	}
	defer db.Close()

	samples := []struct {
		label string
		query string
	}{
		{
			label: "Membership Join",
			query: `
				SELECT id, thread_id, timestamp
				FROM events
				WHERE channel = 'imessage'
				  AND content_types LIKE '%"membership"%'
				  AND metadata_json LIKE '%"action":"added"%'
				ORDER BY timestamp DESC
				LIMIT 1
			`,
		},
		{
			label: "Membership Leave",
			query: `
				SELECT id, thread_id, timestamp
				FROM events
				WHERE channel = 'imessage'
				  AND content_types LIKE '%"membership"%'
				  AND metadata_json LIKE '%"action":"removed"%'
				ORDER BY timestamp DESC
				LIMIT 1
			`,
		},
		{
			label: "Reaction",
			query: `
				SELECT id, thread_id, timestamp
				FROM events
				WHERE channel = 'imessage'
				  AND content_types LIKE '%"reaction"%'
				  AND reply_to IS NOT NULL AND reply_to != ''
				ORDER BY timestamp DESC
				LIMIT 1
			`,
		},
		{
			label: "Image Attachment",
			query: `
				SELECT e.id, e.thread_id, e.timestamp
				FROM events e
				JOIN attachments a ON a.event_id = e.id
				WHERE e.channel = 'imessage'
				  AND a.media_type = 'image'
				ORDER BY e.timestamp DESC
				LIMIT 1
			`,
		},
	}

	for _, sample := range samples {
		var eventID, threadID string
		var timestamp int64
		if err := db.QueryRow(sample.query).Scan(&eventID, &threadID, &timestamp); err != nil {
			fmt.Printf("=== %s ===\n", sample.label)
			fmt.Println("No matching event found.\n")
			continue
		}

		events, err := fetchWindowEvents(db, threadID, timestamp, 15, 15)
		if err != nil {
			fmt.Printf("=== %s ===\n", sample.label)
			fmt.Printf("Error fetching window: %v\n\n", err)
			continue
		}

		threadName, channel := getThreadInfo(db, threadID)
		participants := deriveParticipants(events)

		fmt.Printf("=== %s ===\n", sample.label)
		fmt.Printf("<EPISODE_CONTEXT>\n")
		if threadName != "" {
			fmt.Printf("Thread: %s (%s, group)\n", threadName, channel)
		} else {
			fmt.Printf("Thread: (%s, group)\n", channel)
		}
		if len(participants) > 0 {
			fmt.Printf("Participants: %s\n", strings.Join(participants, ", "))
		}
		fmt.Printf("</EPISODE_CONTEXT>\n\n")

		fmt.Printf("<MESSAGES>\n")
		fmt.Print(encodeEvents(events))
		fmt.Printf("</MESSAGES>\n\n")
	}
}

func getCortexDBPath() (string, error) {
	dataDir, err := config.GetDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataDir, "cortex.db"), nil
}

func fetchWindowEvents(db *sql.DB, threadID string, centerTS int64, before, after int) ([]eventRow, error) {
	beforeEvents, err := fetchEvents(db, threadID, centerTS, before, true)
	if err != nil {
		return nil, err
	}
	afterEvents, err := fetchEvents(db, threadID, centerTS, after, false)
	if err != nil {
		return nil, err
	}

	// reverse beforeEvents to chronological order
	for i, j := 0, len(beforeEvents)-1; i < j; i, j = i+1, j-1 {
		beforeEvents[i], beforeEvents[j] = beforeEvents[j], beforeEvents[i]
	}

	events := append(beforeEvents, afterEvents...)
	return events, nil
}

func fetchEvents(db *sql.DB, threadID string, centerTS int64, limit int, includeCenter bool) ([]eventRow, error) {
	operator := "<"
	order := "DESC"
	if includeCenter {
		operator = "<="
	}
	if !includeCenter {
		operator = ">"
		order = "ASC"
	}

	query := fmt.Sprintf(`
		SELECT
			e.id,
			e.content,
			e.timestamp,
			e.content_types,
			e.metadata_json,
			e.reply_to,
			COALESCE(p.canonical_name, p.display_name, c.display_name,
				CASE e.direction WHEN 'sent' THEN 'Me' ELSE 'Unknown' END) as sender,
			(
				SELECT GROUP_CONCAT(
					COALESCE(mp.canonical_name, mp.display_name, mc.display_name, 'Unknown'), '|'
				)
				FROM event_participants mem
				LEFT JOIN contacts mc ON mem.contact_id = mc.id
				LEFT JOIN persons mp ON mp.id = (
					SELECT person_id FROM person_contact_links pcl
					WHERE pcl.contact_id = mem.contact_id
					ORDER BY confidence DESC, last_seen_at DESC
					LIMIT 1
				)
				WHERE mem.event_id = e.id AND mem.role = 'member'
			) as members,
			(
				SELECT GROUP_CONCAT(
					CASE
						WHEN a.media_type = 'image' THEN 'image'
						WHEN a.media_type = 'video' THEN 'video'
						WHEN a.media_type = 'audio' THEN 'audio'
						WHEN a.media_type = 'sticker' THEN 'sticker'
						ELSE COALESCE(a.filename, 'file') || '::' || COALESCE(a.mime_type, '')
					END, '|'
				)
				FROM attachments a WHERE a.event_id = e.id
			) as attachments
		FROM events e
		LEFT JOIN event_participants ep ON e.id = ep.event_id AND ep.role = 'sender'
		LEFT JOIN contacts c ON ep.contact_id = c.id
		LEFT JOIN persons p ON p.id = (
			SELECT person_id FROM person_contact_links pcl
			WHERE pcl.contact_id = ep.contact_id
			ORDER BY confidence DESC, last_seen_at DESC
			LIMIT 1
		)
		WHERE e.thread_id = ?
		  AND e.timestamp %s ?
		ORDER BY e.timestamp %s
		LIMIT ?
	`, operator, order)

	rows, err := db.Query(query, threadID, centerTS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []eventRow
	for rows.Next() {
		var ev eventRow
		var ts int64
		if err := rows.Scan(&ev.ID, &ev.Content, &ts, &ev.ContentTypes, &ev.MetadataJSON, &ev.ReplyTo, &ev.Sender, &ev.Members, &ev.Attachments); err != nil {
			return nil, err
		}
		ev.Timestamp = time.Unix(ts, 0)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func encodeEvents(events []eventRow) string {
	var sb strings.Builder
	messageSnippets := map[string]string{}

	for _, ev := range events {
		timestamp := ev.Timestamp.UTC().Format(time.RFC3339)

		if hasContentType(ev.ContentTypes, "reaction") {
			emoji := strings.TrimSpace(ev.Content)
			if emoji == "" {
				continue
			}
			snippet := ""
			if ev.ReplyTo.Valid {
				if candidate, ok := messageSnippets[ev.ReplyTo.String]; ok {
					snippet = candidate
				}
			}
			if snippet != "" {
				sb.WriteString(fmt.Sprintf("  -> %s %s to \"%s\"\n", ev.Sender, emoji, snippet))
			} else {
				sb.WriteString(fmt.Sprintf("  -> %s reacted %s\n", ev.Sender, emoji))
			}
			continue
		}

		if hasContentType(ev.ContentTypes, "membership") {
			line := formatMembershipLine(ev.Sender, ev.MetadataJSON, ev.Members)
			if line != "" {
				sb.WriteString(line)
				sb.WriteString("\n")
			}
			continue
		}

		var parts []string
		if strings.TrimSpace(ev.Content) != "" {
			parts = append(parts, ev.Content)
		}
		if ev.Attachments.Valid && ev.Attachments.String != "" {
			for _, att := range strings.Split(ev.Attachments.String, "|") {
				switch att {
				case "image":
					parts = append(parts, "[Image]")
				case "video":
					parts = append(parts, "[Video]")
				case "audio":
					parts = append(parts, "[Audio]")
				case "sticker":
					parts = append(parts, "[Sticker]")
				default:
					fileName, mimeType := splitAttachmentDescriptor(att)
					if mimeType != "" {
						parts = append(parts, fmt.Sprintf("[Attachment] %s (%s)", fileName, mimeType))
					} else {
						parts = append(parts, fmt.Sprintf("[Attachment] %s", fileName))
					}
				}
			}
		}

		if len(parts) == 0 {
			continue
		}

		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", timestamp, ev.Sender, strings.Join(parts, " ")))

		snippet := reactionSnippet(ev.Content)
		if snippet != "" {
			messageSnippets[ev.ID] = snippet
		}
	}

	return sb.String()
}

func getThreadInfo(db *sql.DB, threadID string) (string, string) {
	var name, channel sql.NullString
	_ = db.QueryRow(`SELECT name, channel FROM threads WHERE id = ?`, threadID).Scan(&name, &channel)
	threadName := ""
	if name.Valid {
		threadName = name.String
	}
	threadChannel := "unknown"
	if channel.Valid {
		threadChannel = channel.String
	}
	return threadName, threadChannel
}

func deriveParticipants(events []eventRow) []string {
	seen := make(map[string]struct{})
	for _, ev := range events {
		name := strings.TrimSpace(ev.Sender)
		if name != "" && name != "Unknown" && name != "Me" {
			seen[name] = struct{}{}
		}
		for _, member := range splitPipeList(ev.Members) {
			seen[member] = struct{}{}
		}
	}
	participants := make([]string, 0, len(seen))
	for name := range seen {
		participants = append(participants, name)
	}
	sort.Strings(participants)
	return participants
}

func hasContentType(contentTypesJSON, target string) bool {
	if contentTypesJSON == "" {
		return false
	}
	return strings.Contains(contentTypesJSON, target)
}

func formatMembershipLine(actor string, metadataJSON sql.NullString, members sql.NullString) string {
	action := parseMembershipAction(metadataJSON)
	memberNames := splitPipeList(members)

	actor = strings.TrimSpace(actor)
	if actor == "Unknown" {
		actor = ""
	}
	memberList := strings.Join(memberNames, ", ")

	switch action {
	case "added":
		if memberList == "" {
			return "-> member joined"
		}
		if actor != "" {
			return fmt.Sprintf("-> %s added %s", actor, memberList)
		}
		return fmt.Sprintf("-> %s joined", memberList)
	case "removed":
		if memberList == "" {
			return "-> member left"
		}
		if actor != "" && actor != memberList {
			return fmt.Sprintf("-> %s removed %s", actor, memberList)
		}
		return fmt.Sprintf("-> %s left", memberList)
	default:
		if memberList == "" {
			return ""
		}
		return fmt.Sprintf("-> membership update: %s", memberList)
	}
}

func parseMembershipAction(metadataJSON sql.NullString) string {
	if !metadataJSON.Valid || metadataJSON.String == "" {
		return "unknown"
	}
	if strings.Contains(metadataJSON.String, `"action":"added"`) {
		return "added"
	}
	if strings.Contains(metadataJSON.String, `"action":"removed"`) {
		return "removed"
	}
	return "unknown"
}

func splitPipeList(values sql.NullString) []string {
	if !values.Valid || values.String == "" {
		return nil
	}
	raw := strings.Split(values.String, "|")
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item != "" && item != "Unknown" {
			out = append(out, item)
		}
	}
	return out
}

func splitAttachmentDescriptor(att string) (string, string) {
	parts := strings.SplitN(att, "::", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(att), ""
}

func reactionSnippet(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	const maxRunes = 80
	runes := []rune(trimmed)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return trimmed
}
