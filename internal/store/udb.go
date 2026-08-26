package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/maniginam/waggle/internal/model"
	"github.com/maniginam/waggle/pkg/id"
)

var (
	ErrUnknownColumn     = errors.New("unknown column")
	ErrInvalidCell       = errors.New("invalid cell value for column type")
	ErrInvalidColumnType = errors.New("invalid column type")
	ErrInvalidView       = errors.New("invalid view kind")
	ErrInvalidViewConfig = errors.New("invalid view config")
)

const dbCols = "id, project_id, name, created_at, updated_at"

func scanDatabase(row scanner) (*model.Database, error) {
	var d model.Database
	var c, u string
	err := row.Scan(&d.ID, &d.ProjectID, &d.Name, &c, &u)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339, c)
	d.UpdatedAt, _ = time.Parse(time.RFC3339, u)
	return &d, nil
}

func (s *Store) CreateDatabase(d *model.Database) error {
	if d.ID == "" {
		d.ID = id.New()
	}
	now := time.Now().UTC()
	d.CreatedAt, d.UpdatedAt = now, now
	_, err := s.db.Exec("INSERT INTO udb ("+dbCols+") VALUES (?,?,?,?,?)",
		d.ID, d.ProjectID, d.Name,
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	return err
}

func (s *Store) GetDatabase(dbID string) (*model.Database, error) {
	return scanDatabase(s.db.QueryRow("SELECT "+dbCols+" FROM udb WHERE id = ?", dbID))
}

func (s *Store) ListDatabases(projectID string) ([]*model.Database, error) {
	rows, err := s.db.Query("SELECT "+dbCols+" FROM udb WHERE project_id = ? ORDER BY created_at ASC", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Database
	for rows.Next() {
		d, err := scanDatabase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) RenameDatabase(dbID, name string) (*model.Database, error) {
	res, err := s.db.Exec("UPDATE udb SET name = ?, updated_at = ? WHERE id = ?",
		name, time.Now().UTC().Format(time.RFC3339), dbID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.GetDatabase(dbID)
}

func (s *Store) DeleteDatabase(dbID string) error {
	if _, err := s.GetDatabase(dbID); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, q := range []string{
		"DELETE FROM udb_view WHERE db_id = ?",
		"DELETE FROM udb_row WHERE db_id = ?",
		"DELETE FROM udb_column WHERE db_id = ?",
		"DELETE FROM udb WHERE id = ?",
	} {
		if _, err := tx.Exec(q, dbID); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func validColumnType(t string) bool {
	switch t {
	case model.ColumnTypeText, model.ColumnTypeNumber, model.ColumnTypeDate,
		model.ColumnTypeSelect, model.ColumnTypeCheckbox:
		return true
	}
	return false
}

const colCols = "id, db_id, name, type, position, options_json, created_at"

func scanColumn(row scanner) (*model.Column, error) {
	var c model.Column
	var opts, created string
	err := row.Scan(&c.ID, &c.DBID, &c.Name, &c.Type, &c.Position, &opts, &created)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if opts != "" {
		json.Unmarshal([]byte(opts), &c.Options)
	}
	c.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return &c, nil
}

func (s *Store) AddColumn(c *model.Column) error {
	if !validColumnType(c.Type) {
		return ErrInvalidColumnType
	}
	if c.ID == "" {
		c.ID = id.New()
	}
	c.CreatedAt = time.Now().UTC()
	optsJSON := ""
	if len(c.Options) > 0 {
		b, _ := json.Marshal(c.Options)
		optsJSON = string(b)
	}
	_, err := s.db.Exec("INSERT INTO udb_column ("+colCols+") VALUES (?,?,?,?,?,?,?)",
		c.ID, c.DBID, c.Name, c.Type, c.Position, optsJSON, c.CreatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) GetColumn(colID string) (*model.Column, error) {
	return scanColumn(s.db.QueryRow("SELECT "+colCols+" FROM udb_column WHERE id = ?", colID))
}

func (s *Store) ListColumns(dbID string) ([]*model.Column, error) {
	rows, err := s.db.Query("SELECT "+colCols+" FROM udb_column WHERE db_id = ? ORDER BY position ASC, created_at ASC", dbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Column
	for rows.Next() {
		c, err := scanColumn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) UpdateColumn(colID string, updates map[string]any) (*model.Column, error) {
	if _, err := s.GetColumn(colID); err != nil {
		return nil, err
	}
	var sets []string
	var args []any
	for k, v := range updates {
		switch k {
		case "name", "position":
			sets = append(sets, k+" = ?")
			args = append(args, v)
		case "options":
			b, _ := json.Marshal(v)
			sets = append(sets, "options_json = ?")
			args = append(args, string(b))
		}
	}
	if len(sets) == 0 {
		return s.GetColumn(colID)
	}
	args = append(args, colID)
	if _, err := s.db.Exec("UPDATE udb_column SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...); err != nil {
		return nil, err
	}
	return s.GetColumn(colID)
}

func (s *Store) DeleteColumn(colID string) error {
	res, err := s.db.Exec("DELETE FROM udb_column WHERE id = ?", colID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func validateCellValue(c *model.Column, v any) error {
	if v == nil {
		return nil // unset cell allowed
	}
	switch c.Type {
	case model.ColumnTypeText:
		if _, ok := v.(string); !ok {
			return ErrInvalidCell
		}
	case model.ColumnTypeNumber:
		switch v.(type) {
		case float64, float32, int, int64:
		default:
			return ErrInvalidCell
		}
	case model.ColumnTypeCheckbox:
		if _, ok := v.(bool); !ok {
			return ErrInvalidCell
		}
	case model.ColumnTypeDate:
		s, ok := v.(string)
		if !ok {
			return ErrInvalidCell
		}
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return ErrInvalidCell
		}
	case model.ColumnTypeSelect:
		s, ok := v.(string)
		if !ok {
			return ErrInvalidCell
		}
		for _, o := range c.Options {
			if o.Value == s {
				return nil
			}
		}
		return ErrInvalidCell
	default:
		return ErrInvalidColumnType
	}
	return nil
}

func (s *Store) validateRowData(dbID string, data map[string]any) error {
	cols, err := s.ListColumns(dbID)
	if err != nil {
		return err
	}
	byID := make(map[string]*model.Column, len(cols))
	for _, c := range cols {
		byID[c.ID] = c
	}
	for k, v := range data {
		c, ok := byID[k]
		if !ok {
			return ErrUnknownColumn
		}
		if err := validateCellValue(c, v); err != nil {
			return err
		}
	}
	return nil
}

const rowCols = "id, db_id, data_json, position, created_at, updated_at"

func scanRow(row scanner) (*model.Row, error) {
	var r model.Row
	var data, c, u string
	err := row.Scan(&r.ID, &r.DBID, &data, &r.Position, &c, &u)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.Data = map[string]any{}
	if data != "" {
		json.Unmarshal([]byte(data), &r.Data)
	}
	r.CreatedAt, _ = time.Parse(time.RFC3339, c)
	r.UpdatedAt, _ = time.Parse(time.RFC3339, u)
	return &r, nil
}

func (s *Store) CreateRow(r *model.Row) error {
	if err := s.validateRowData(r.DBID, r.Data); err != nil {
		return err
	}
	if r.ID == "" {
		r.ID = id.New()
	}
	now := time.Now().UTC()
	r.CreatedAt, r.UpdatedAt = now, now
	if r.Data == nil {
		r.Data = map[string]any{}
	}
	b, _ := json.Marshal(r.Data)
	_, err := s.db.Exec("INSERT INTO udb_row ("+rowCols+") VALUES (?,?,?,?,?,?)",
		r.ID, r.DBID, string(b), r.Position,
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	return err
}

func (s *Store) GetRow(rowID string) (*model.Row, error) {
	return scanRow(s.db.QueryRow("SELECT "+rowCols+" FROM udb_row WHERE id = ?", rowID))
}

func (s *Store) ListRows(dbID string) ([]*model.Row, error) {
	rows, err := s.db.Query("SELECT "+rowCols+" FROM udb_row WHERE db_id = ? ORDER BY position ASC, created_at ASC", dbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Row
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateRow merges the provided keys into the row's data (partial update).
func (s *Store) UpdateRow(rowID string, data map[string]any) (*model.Row, error) {
	existing, err := s.GetRow(rowID)
	if err != nil {
		return nil, err
	}
	if err := s.validateRowData(existing.DBID, data); err != nil {
		return nil, err
	}
	for k, v := range data {
		existing.Data[k] = v
	}
	b, _ := json.Marshal(existing.Data)
	if _, err := s.db.Exec("UPDATE udb_row SET data_json = ?, updated_at = ? WHERE id = ?",
		string(b), time.Now().UTC().Format(time.RFC3339), rowID); err != nil {
		return nil, err
	}
	return s.GetRow(rowID)
}

func (s *Store) DeleteRow(rowID string) error {
	res, err := s.db.Exec("DELETE FROM udb_row WHERE id = ?", rowID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) columnByID(dbID, colID string) *model.Column {
	cols, err := s.ListColumns(dbID)
	if err != nil {
		return nil
	}
	for _, c := range cols {
		if c.ID == colID {
			return c
		}
	}
	return nil
}

func (s *Store) validateView(v *model.View) error {
	switch v.Kind {
	case model.ViewKindTable:
		return nil
	case model.ViewKindKanban:
		c := s.columnByID(v.DBID, v.Config.GroupBy)
		if c == nil || c.Type != model.ColumnTypeSelect {
			return ErrInvalidViewConfig
		}
		return nil
	case model.ViewKindCalendar:
		c := s.columnByID(v.DBID, v.Config.DateField)
		if c == nil || c.Type != model.ColumnTypeDate {
			return ErrInvalidViewConfig
		}
		return nil
	default:
		return ErrInvalidView
	}
}

const viewCols = "id, db_id, name, kind, config_json, position"

func scanView(row scanner) (*model.View, error) {
	var v model.View
	var cfg string
	err := row.Scan(&v.ID, &v.DBID, &v.Name, &v.Kind, &cfg, &v.Position)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if cfg != "" {
		json.Unmarshal([]byte(cfg), &v.Config)
	}
	return &v, nil
}

func (s *Store) CreateView(v *model.View) error {
	if err := s.validateView(v); err != nil {
		return err
	}
	if v.ID == "" {
		v.ID = id.New()
	}
	b, _ := json.Marshal(v.Config)
	_, err := s.db.Exec("INSERT INTO udb_view ("+viewCols+") VALUES (?,?,?,?,?,?)",
		v.ID, v.DBID, v.Name, v.Kind, string(b), v.Position)
	return err
}

func (s *Store) GetView(viewID string) (*model.View, error) {
	return scanView(s.db.QueryRow("SELECT "+viewCols+" FROM udb_view WHERE id = ?", viewID))
}

func (s *Store) ListViews(dbID string) ([]*model.View, error) {
	rows, err := s.db.Query("SELECT "+viewCols+" FROM udb_view WHERE db_id = ? ORDER BY position ASC", dbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.View
	for rows.Next() {
		v, err := scanView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) UpdateView(viewID string, updates map[string]any) (*model.View, error) {
	existing, err := s.GetView(viewID)
	if err != nil {
		return nil, err
	}
	if name, ok := updates["name"].(string); ok {
		existing.Name = name
	}
	if pos, ok := updates["position"].(float64); ok {
		existing.Position = int(pos)
	}
	if cfg, ok := updates["config"]; ok {
		b, _ := json.Marshal(cfg)
		json.Unmarshal(b, &existing.Config)
	}
	if err := s.validateView(existing); err != nil {
		return nil, err
	}
	cfgB, _ := json.Marshal(existing.Config)
	if _, err := s.db.Exec("UPDATE udb_view SET name = ?, config_json = ?, position = ? WHERE id = ?",
		existing.Name, string(cfgB), existing.Position, viewID); err != nil {
		return nil, err
	}
	return s.GetView(viewID)
}

func (s *Store) DeleteView(viewID string) error {
	res, err := s.db.Exec("DELETE FROM udb_view WHERE id = ?", viewID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
