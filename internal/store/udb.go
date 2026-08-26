package store

import (
	"database/sql"
	"errors"
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
