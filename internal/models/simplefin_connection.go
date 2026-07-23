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
	"errors"
	"time"
)

var ErrNoSimpleFinConnection = errors.New("no SimpleFIN connection configured")

// SimpleFinConnection is a singleton row - the app enforces at most one
// exists at a time (Connect deletes any prior row first).
type SimpleFinConnection struct {
	ID                 int
	EncryptedAccessURL []byte
	ConnectedAt        time.Time
	LastSyncedAt       sql.NullTime
	LastSyncError      sql.NullString
}

type SimpleFinConnectionStore struct {
	DB *sql.DB
}

func (s *SimpleFinConnectionStore) Get(ctx context.Context) (*SimpleFinConnection, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, encrypted_access_url, connected_at, last_synced_at, last_sync_error
		FROM bt_simplefin_connection LIMIT 1`)
	var c SimpleFinConnection
	err := row.Scan(&c.ID, &c.EncryptedAccessURL, &c.ConnectedAt, &c.LastSyncedAt, &c.LastSyncError)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSimpleFinConnection
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Connect replaces any existing connection with a new one - only one
// SimpleFIN Bridge connection is supported at a time.
func (s *SimpleFinConnectionStore) Connect(ctx context.Context, encryptedAccessURL []byte) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM bt_simplefin_connection`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO bt_simplefin_connection (encrypted_access_url) VALUES ($1)`, encryptedAccessURL); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SimpleFinConnectionStore) Disconnect(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM bt_simplefin_connection`)
	return err
}

func (s *SimpleFinConnectionStore) MarkSynced(ctx context.Context, id int, syncErr error) error {
	var errText sql.NullString
	if syncErr != nil {
		errText = sql.NullString{String: syncErr.Error(), Valid: true}
	}
	_, err := s.DB.ExecContext(ctx, `
		UPDATE bt_simplefin_connection SET last_synced_at = now(), last_sync_error = $2 WHERE id = $1`,
		id, errText)
	return err
}
