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

func TestRowCRUDAndValidation(t *testing.T) {
	s := tempStore(t)
	db := &model.Database{Name: "T"}
	s.CreateDatabase(db)
	name := &model.Column{DBID: db.ID, Name: "Name", Type: model.ColumnTypeText}
	age := &model.Column{DBID: db.ID, Name: "Age", Type: model.ColumnTypeNumber}
	done := &model.Column{DBID: db.ID, Name: "Done", Type: model.ColumnTypeCheckbox}
	s.AddColumn(name)
	s.AddColumn(age)
	s.AddColumn(done)

	row := &model.Row{DBID: db.ID, Data: map[string]any{name.ID: "Ada", age.ID: 36.0, done.ID: true}}
	if err := s.CreateRow(row); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _ := s.GetRow(row.ID)
	if got.Data[name.ID] != "Ada" || got.Data[age.ID].(float64) != 36 {
		t.Fatalf("roundtrip wrong: %+v", got.Data)
	}

	// unknown column key
	if err := s.CreateRow(&model.Row{DBID: db.ID, Data: map[string]any{"nope": "x"}}); err != ErrUnknownColumn {
		t.Fatalf("expected ErrUnknownColumn, got %v", err)
	}
	// wrong type: number column gets a string
	if err := s.CreateRow(&model.Row{DBID: db.ID, Data: map[string]any{age.ID: "old"}}); err != ErrInvalidCell {
		t.Fatalf("expected ErrInvalidCell, got %v", err)
	}

	if _, err := s.UpdateRow(row.ID, map[string]any{name.ID: "Grace"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = s.GetRow(row.ID)
	if got.Data[name.ID] != "Grace" {
		t.Fatalf("update not applied: %+v", got.Data)
	}
	if err := s.DeleteRow(row.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if rows, _ := s.ListRows(db.ID); len(rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(rows))
	}
}

func TestDeleteColumnLeavesRowDataInert(t *testing.T) {
	s := tempStore(t)
	db := &model.Database{Name: "T"}
	s.CreateDatabase(db)
	c := &model.Column{DBID: db.ID, Name: "Name", Type: model.ColumnTypeText}
	s.AddColumn(c)
	s.CreateRow(&model.Row{DBID: db.ID, Data: map[string]any{c.ID: "keep"}})
	if err := s.DeleteColumn(c.ID); err != nil {
		t.Fatalf("delete col: %v", err)
	}
	// Row still loads; the stale key is simply ignored by callers.
	rows, err := s.ListRows(db.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("row should survive column delete: %v len=%d", err, len(rows))
	}
}

func TestCellValidationDateAndSelect(t *testing.T) {
	s := tempStore(t)
	db := &model.Database{Name: "T"}
	s.CreateDatabase(db)
	date := &model.Column{DBID: db.ID, Name: "When", Type: model.ColumnTypeDate}
	status := &model.Column{DBID: db.ID, Name: "Status", Type: model.ColumnTypeSelect,
		Options: []model.SelectOption{{Value: "Todo"}, {Value: "Done"}}}
	s.AddColumn(date)
	s.AddColumn(status)

	// date: valid ISO date succeeds
	if err := s.CreateRow(&model.Row{DBID: db.ID, Data: map[string]any{date.ID: "2026-01-15"}}); err != nil {
		t.Fatalf("valid date: %v", err)
	}
	// date: unparseable string rejected
	if err := s.CreateRow(&model.Row{DBID: db.ID, Data: map[string]any{date.ID: "nope"}}); err != ErrInvalidCell {
		t.Fatalf("expected ErrInvalidCell for bad date, got %v", err)
	}
	// date: out-of-range string rejected
	if err := s.CreateRow(&model.Row{DBID: db.ID, Data: map[string]any{date.ID: "2026-13-40"}}); err != ErrInvalidCell {
		t.Fatalf("expected ErrInvalidCell for out-of-range date, got %v", err)
	}

	// select: matching option succeeds
	if err := s.CreateRow(&model.Row{DBID: db.ID, Data: map[string]any{status.ID: "Todo"}}); err != nil {
		t.Fatalf("valid select: %v", err)
	}
	// select: non-matching value rejected
	if err := s.CreateRow(&model.Row{DBID: db.ID, Data: map[string]any{status.ID: "Nope"}}); err != ErrInvalidCell {
		t.Fatalf("expected ErrInvalidCell for bad select, got %v", err)
	}
	// select: non-string value rejected
	if err := s.CreateRow(&model.Row{DBID: db.ID, Data: map[string]any{status.ID: 42}}); err != ErrInvalidCell {
		t.Fatalf("expected ErrInvalidCell for non-string select, got %v", err)
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

func TestViewCRUDAndValidation(t *testing.T) {
	s := tempStore(t)
	db := &model.Database{Name: "T"}
	s.CreateDatabase(db)
	sel := &model.Column{DBID: db.ID, Name: "Stage", Type: model.ColumnTypeSelect,
		Options: []model.SelectOption{{Value: "A"}}}
	dt := &model.Column{DBID: db.ID, Name: "Due", Type: model.ColumnTypeDate}
	s.AddColumn(sel)
	s.AddColumn(dt)

	tbl := &model.View{DBID: db.ID, Name: "All", Kind: model.ViewKindTable}
	if err := s.CreateView(tbl); err != nil {
		t.Fatalf("table view: %v", err)
	}
	kan := &model.View{DBID: db.ID, Name: "Board", Kind: model.ViewKindKanban,
		Config: model.ViewConfig{GroupBy: sel.ID}}
	if err := s.CreateView(kan); err != nil {
		t.Fatalf("kanban view: %v", err)
	}
	cal := &model.View{DBID: db.ID, Name: "Cal", Kind: model.ViewKindCalendar,
		Config: model.ViewConfig{DateField: dt.ID}}
	if err := s.CreateView(cal); err != nil {
		t.Fatalf("calendar view: %v", err)
	}

	if err := s.CreateView(&model.View{DBID: db.ID, Name: "X", Kind: "grid"}); err != ErrInvalidView {
		t.Fatalf("expected ErrInvalidView, got %v", err)
	}
	// kanban grouped on a non-select column
	if err := s.CreateView(&model.View{DBID: db.ID, Name: "Y", Kind: model.ViewKindKanban,
		Config: model.ViewConfig{GroupBy: dt.ID}}); err != ErrInvalidViewConfig {
		t.Fatalf("expected ErrInvalidViewConfig, got %v", err)
	}
	// calendar on a non-date column
	if err := s.CreateView(&model.View{DBID: db.ID, Name: "Z", Kind: model.ViewKindCalendar,
		Config: model.ViewConfig{DateField: sel.ID}}); err != ErrInvalidViewConfig {
		t.Fatalf("expected ErrInvalidViewConfig, got %v", err)
	}

	views, _ := s.ListViews(db.ID)
	if len(views) != 3 {
		t.Fatalf("expected 3 views, got %d", len(views))
	}
	if _, err := s.UpdateView(kan.ID, map[string]any{"name": "Pipeline"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	// re-validation: regrouping the kanban onto a date column is rejected
	// and must not mutate the persisted config.
	if _, err := s.UpdateView(kan.ID, map[string]any{
		"config": map[string]any{"group_by": dt.ID}}); err != ErrInvalidViewConfig {
		t.Fatalf("expected ErrInvalidViewConfig on bad config update, got %v", err)
	}
	if got, err := s.GetView(kan.ID); err != nil || got.Config.GroupBy != sel.ID {
		t.Fatalf("rejected update mutated stored config: err=%v group_by=%q", err, got.Config.GroupBy)
	}
	if err := s.DeleteView(tbl.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if views, _ := s.ListViews(db.ID); len(views) != 2 {
		t.Fatalf("expected 2 views, got %d", len(views))
	}
}
