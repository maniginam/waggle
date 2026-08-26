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
