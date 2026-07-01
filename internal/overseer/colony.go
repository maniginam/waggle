package overseer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/maniginam/waggle/internal/model"
	_ "modernc.org/sqlite"
)

type ColonySource struct {
	dbPath string
	limit  int
}

func NewColonySource(dbPath string) *ColonySource {
	return &ColonySource{dbPath: dbPath, limit: 50}
}

func (c *ColonySource) Name() string { return "colony" }

func (c *ColonySource) Poll(ctx context.Context) (Snapshot, error) {
	db, err := sql.Open("sqlite", "file:"+c.dbPath+"?mode=ro&_busy_timeout=2000")
	if err != nil {
		log.Printf("overseer: colony open %s: %v", c.dbPath, err)
		return Snapshot{}, nil // degrade to empty, never fatal
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT id, type, priority, status, payload, started_at, worker_pid
		FROM tasks
		ORDER BY coalesce(started_at, created_at) DESC
		LIMIT ?`, c.limit)
	if err != nil {
		log.Printf("overseer: colony query: %v", err)
		return Snapshot{}, nil
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		var id, typ, prio, status string
		var payload, startedAt sql.NullString
		var workerPID sql.NullInt64
		if err := rows.Scan(&id, &typ, &prio, &status, &payload, &startedAt, &workerPID); err != nil {
			continue
		}
		items = append(items, colonyItem(id, typ, prio, status, payload.String, startedAt.String, workerPID))
	}
	return Snapshot{Items: items}, nil
}

func colonyItem(id, typ, prio, status, payload, startedAt string, workerPID sql.NullInt64) Item {
	pl := map[string]any{"source": "colony", "type": typ, "priority": prio}
	if payload != "" {
		var p struct {
			Project string `json:"project"`
		}
		if json.Unmarshal([]byte(payload), &p) == nil && p.Project != "" {
			pl["project"] = p.Project
		}
	}

	var et model.EventType
	switch {
	case typ == "roi-brain" && (status == "running" || status == "queued"):
		et = "brain.cycle"
	case status == "running":
		et = "worker.running"
		pl["started_at"] = startedAt
		if workerPID.Valid {
			pl["worker_pid"] = workerPID.Int64
		}
	default:
		et = model.EventType("task." + status)
	}

	return Item{
		Key: fmt.Sprintf("colony:%s:%s", id, status),
		Event: &model.Event{
			Type:      et,
			TaskID:    id,
			Payload:   pl,
			Timestamp: time.Now(),
		},
	}
}
