package store

import (
	"testing"

	"github.com/maniginam/waggle/internal/model"
)

func TestDatabaseCRUD(t *testing.T) {
	s := tempStore(t)
	db := &model.Database{ProjectID: "p1", Name: "Contacts"}
	if err := s.CreateDatabase(db); err != nil {
		t.Fatalf("create: %v", err)
	}
	if db.ID == "" {
		t.Fatal("expected generated id")
	}
	got, err := s.GetDatabase(db.ID)
	if err != nil || got.Name != "Contacts" {
		t.Fatalf("get: %v name=%q", err, got.Name)
	}
	list, err := s.ListDatabases("p1")
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}
	if _, err := s.RenameDatabase(db.ID, "People"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, _ = s.GetDatabase(db.ID)
	if got.Name != "People" {
		t.Fatalf("rename not applied: %q", got.Name)
	}
	if err := s.DeleteDatabase(db.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetDatabase(db.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestColumnCRUD(t *testing.T) {
	s := tempStore(t)
	db := &model.Database{Name: "T"}
	s.CreateDatabase(db)

	col := &model.Column{DBID: db.ID, Name: "Status", Type: model.ColumnTypeSelect,
		Options: []model.SelectOption{{Value: "Todo"}, {Value: "Done"}}}
	if err := s.AddColumn(col); err != nil {
		t.Fatalf("add: %v", err)
	}
	if col.ID == "" {
		t.Fatal("expected id")
	}
	cols, _ := s.ListColumns(db.ID)
	if len(cols) != 1 || cols[0].Type != model.ColumnTypeSelect || len(cols[0].Options) != 2 {
		t.Fatalf("list roundtrip wrong: %+v", cols)
	}

	bad := &model.Column{DBID: db.ID, Name: "X", Type: "bogus"}
	if err := s.AddColumn(bad); err != ErrInvalidColumnType {
		t.Fatalf("expected ErrInvalidColumnType, got %v", err)
	}

	if _, err := s.UpdateColumn(col.ID, map[string]any{"name": "State"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := s.GetColumn(col.ID)
	if got.Name != "State" {
		t.Fatalf("update not applied: %q", got.Name)
	}
	if err := s.DeleteColumn(col.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if cols, _ := s.ListColumns(db.ID); len(cols) != 0 {
		t.Fatalf("expected 0 columns, got %d", len(cols))
	}
}
