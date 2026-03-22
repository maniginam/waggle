package store

import "time"

func (s *Store) CleanupEvents(retentionDays int) (int64, error) {
	cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour).Format(time.RFC3339)
	result, err := s.db.Exec("DELETE FROM events WHERE timestamp < ?", cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) CleanupMessages(readRetentionDays int) (int64, error) {
	cutoff := time.Now().UTC().Add(-time.Duration(readRetentionDays) * 24 * time.Hour).Format(time.RFC3339)
	result, err := s.db.Exec("DELETE FROM messages WHERE read = 1 AND created_at < ?", cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
