package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"

	_ "github.com/mattn/go-sqlite3"
)

const defaultDBPath = "/Users/tyler/Library/Application Support/Cortex/cortex.db"

func main() {
	runID := flag.String("run-id", "", "Run ID to delete (required)")
	dbPath := flag.String("db", defaultDBPath, "SQLite DB to clean")
	dryRun := flag.Bool("dry-run", false, "Show counts without deleting")
	flag.Parse()

	if *runID == "" {
		fmt.Fprintln(os.Stderr, "Error: --run-id is required")
		os.Exit(2)
	}

	db, err := sql.Open("sqlite3", *dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening db: %v\n", err)
		os.Exit(2)
	}
	defer db.Close()

	prefix := *runID + ":%"

	if *dryRun {
		printCounts(db, prefix)
		return
	}

	tx, err := db.Begin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting transaction: %v\n", err)
		os.Exit(2)
	}

	deleted := make(map[string]int64)
	deleted["episode_relationship_mentions"] = execDelete(tx, "DELETE FROM episode_relationship_mentions WHERE episode_id LIKE ?", prefix)
	deleted["episode_entity_mentions"] = execDelete(tx, "DELETE FROM episode_entity_mentions WHERE episode_id LIKE ?", prefix)
	deleted["episode_events"] = execDelete(tx, "DELETE FROM episode_events WHERE episode_id LIKE ?", prefix)
	deleted["episodes"] = execDelete(tx, "DELETE FROM episodes WHERE id LIKE ?", prefix)

	deleted["relationships"] = execDelete(tx, `
		DELETE FROM relationships
		WHERE id NOT IN (
			SELECT relationship_id FROM episode_relationship_mentions WHERE relationship_id IS NOT NULL
		)
	`)
	deleted["entities"] = execDelete(tx, `
		DELETE FROM entities
		WHERE id NOT IN (SELECT entity_id FROM episode_entity_mentions)
		  AND id NOT IN (SELECT source_entity_id FROM relationships)
		  AND id NOT IN (SELECT target_entity_id FROM relationships)
	`)
	deleted["entity_aliases"] = execDelete(tx, `
		DELETE FROM entity_aliases
		WHERE entity_id NOT IN (SELECT id FROM entities)
	`)
	deleted["merge_candidates"] = execDelete(tx, `
		DELETE FROM merge_candidates
		WHERE entity_a_id NOT IN (SELECT id FROM entities)
		   OR entity_b_id NOT IN (SELECT id FROM entities)
	`)
	deleted["entity_merge_events"] = execDelete(tx, `
		DELETE FROM entity_merge_events
		WHERE source_entity_id NOT IN (SELECT id FROM entities)
		   OR target_entity_id NOT IN (SELECT id FROM entities)
	`)

	if err := tx.Commit(); err != nil {
		fmt.Fprintf(os.Stderr, "Error committing transaction: %v\n", err)
		os.Exit(2)
	}

	fmt.Printf("Cleanup complete for run ID: %s\n", *runID)
	for table, count := range deleted {
		fmt.Printf("  %s: %d\n", table, count)
	}
}

func execDelete(tx *sql.Tx, query string, args ...interface{}) int64 {
	res, err := tx.Exec(query, args...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Delete failed: %v\nQuery: %s\n", err, query)
		tx.Rollback()
		os.Exit(2)
	}
	affected, _ := res.RowsAffected()
	return affected
}

func printCounts(db *sql.DB, prefix string) {
	counts := map[string]string{
		"episode_relationship_mentions": "SELECT COUNT(*) FROM episode_relationship_mentions WHERE episode_id LIKE ?",
		"episode_entity_mentions":       "SELECT COUNT(*) FROM episode_entity_mentions WHERE episode_id LIKE ?",
		"episode_events":                "SELECT COUNT(*) FROM episode_events WHERE episode_id LIKE ?",
		"episodes":                      "SELECT COUNT(*) FROM episodes WHERE id LIKE ?",
	}

	fmt.Printf("Dry run for run ID prefix: %s\n", prefix)
	for table, query := range counts {
		var count int64
		if err := db.QueryRow(query, prefix).Scan(&count); err != nil {
			fmt.Fprintf(os.Stderr, "Count failed for %s: %v\n", table, err)
			continue
		}
		fmt.Printf("  %s: %d\n", table, count)
	}
	fmt.Println("Note: relationships/entities cleanup requires actual deletion pass.")
}
