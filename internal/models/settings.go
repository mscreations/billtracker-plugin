// Copyright (C) 2026 Jon Shaulis
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package models

import (
	"context"
	"database/sql"
)

// SettingsStore is a plain key/value store, mirroring hhq's own settings
// table, for future parent-editable plugin config.
type SettingsStore struct {
	DB *sql.DB
}

// Get returns the stored value for key, or def if unset.
func (s *SettingsStore) Get(ctx context.Context, key, def string) (string, error) {
	var value string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM bt_settings WHERE key = $1`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return def, nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

// Set creates or updates a setting.
func (s *SettingsStore) Set(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO bt_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value)
	return err
}

// SetIfAbsent inserts value for key only if key doesn't already have a
// stored value, returning whether this call's value actually won. Used for
// the plugin-token self-registration handshake (see internal/handlers.
// Register), where two racing callers must never both believe they set the
// authoritative token - the loser must never learn the winner's value, so
// this deliberately doesn't return what's currently stored on a loss.
func (s *SettingsStore) SetIfAbsent(ctx context.Context, key, value string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO bt_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO NOTHING`, key, value)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
