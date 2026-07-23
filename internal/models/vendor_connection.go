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

var ErrVendorConnectionNotFound = errors.New("vendor connection not found")

// VendorConnection links a bill definition to a vendor bill-pay portal
// login (see internal/connectors) - one per bill.
type VendorConnection struct {
	ID                int
	BillDefinitionID  int
	Connector         string
	Tenant            string
	Username          string
	EncryptedPassword []byte
	LastAccountNumber sql.NullString
	LastSyncedAt      sql.NullTime
	LastSyncError     sql.NullString
	BootstrapManaged  bool
	CreatedAt         time.Time
}

type VendorConnectionStore struct {
	DB *sql.DB
}

func (s *VendorConnectionStore) ListAll(ctx context.Context) ([]VendorConnection, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, bill_definition_id, connector, tenant, username, encrypted_password,
		       last_account_number, last_synced_at, last_sync_error, bootstrap_managed, created_at
		FROM bt_vendor_connections
		ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanVendorConnections(rows)
}

func (s *VendorConnectionStore) GetByBillDefinitionID(ctx context.Context, billDefinitionID int) (*VendorConnection, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, bill_definition_id, connector, tenant, username, encrypted_password,
		       last_account_number, last_synced_at, last_sync_error, bootstrap_managed, created_at
		FROM bt_vendor_connections WHERE bill_definition_id = $1`, billDefinitionID)
	return scanVendorConnection(row)
}

// Upsert creates or replaces the connection for billDefinitionID (one
// connection per bill, so a re-bootstrap or a settings-page re-save
// overwrites the prior row's connector/tenant/username/password wholesale
// rather than trying to reconcile individual fields).
func (s *VendorConnectionStore) Upsert(ctx context.Context, v VendorConnection) (int, error) {
	var id int
	err := s.DB.QueryRowContext(ctx, `
		INSERT INTO bt_vendor_connections (bill_definition_id, connector, tenant, username, encrypted_password, bootstrap_managed)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (bill_definition_id) DO UPDATE SET
			connector = EXCLUDED.connector,
			tenant = EXCLUDED.tenant,
			username = EXCLUDED.username,
			encrypted_password = EXCLUDED.encrypted_password,
			bootstrap_managed = EXCLUDED.bootstrap_managed
		RETURNING id`,
		v.BillDefinitionID, v.Connector, v.Tenant, v.Username, v.EncryptedPassword, v.BootstrapManaged).Scan(&id)
	return id, err
}

func (s *VendorConnectionStore) Delete(ctx context.Context, id int) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM bt_vendor_connections WHERE id = $1`, id)
	return err
}

func (s *VendorConnectionStore) MarkSynced(ctx context.Context, id int, accountNumber string, syncErr error) error {
	var errText sql.NullString
	if syncErr != nil {
		errText = sql.NullString{String: syncErr.Error(), Valid: true}
	}
	acct := sql.NullString{String: accountNumber, Valid: accountNumber != ""}
	_, err := s.DB.ExecContext(ctx, `
		UPDATE bt_vendor_connections
		SET last_synced_at = now(), last_sync_error = $2, last_account_number = COALESCE($3, last_account_number)
		WHERE id = $1`,
		id, errText, acct)
	return err
}

func scanVendorConnections(rows *sql.Rows) ([]VendorConnection, error) {
	var out []VendorConnection
	for rows.Next() {
		var v VendorConnection
		if err := rows.Scan(&v.ID, &v.BillDefinitionID, &v.Connector, &v.Tenant, &v.Username, &v.EncryptedPassword,
			&v.LastAccountNumber, &v.LastSyncedAt, &v.LastSyncError, &v.BootstrapManaged, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func scanVendorConnection(row *sql.Row) (*VendorConnection, error) {
	var v VendorConnection
	err := row.Scan(&v.ID, &v.BillDefinitionID, &v.Connector, &v.Tenant, &v.Username, &v.EncryptedPassword,
		&v.LastAccountNumber, &v.LastSyncedAt, &v.LastSyncError, &v.BootstrapManaged, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrVendorConnectionNotFound
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}
