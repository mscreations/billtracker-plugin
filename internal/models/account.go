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
	"time"
)

// Account is a bank/card account synced from a SimpleFIN Bridge connection.
type Account struct {
	ID                    int
	SimpleFinID           string
	OrgName               sql.NullString
	Name                  string
	DisplayName           sql.NullString
	Currency              string
	BalanceCents          int64
	AvailableBalanceCents sql.NullInt64
	BalanceDate           sql.NullTime
	Visible               bool
	LastSyncedAt          sql.NullTime
	LastSyncError         sql.NullString
	CreatedAt             time.Time
}

// EffectiveName returns the parent-set DisplayName if one is set, otherwise
// the raw SimpleFIN-reported Name.
func (a Account) EffectiveName() string {
	if a.DisplayName.Valid && a.DisplayName.String != "" {
		return a.DisplayName.String
	}
	return a.Name
}

// IsCredit reports whether an account should be grouped with credit
// accounts rather than bank accounts, based on a negative balance.
func (a Account) IsCredit() bool {
	return a.BalanceCents < 0
}

type AccountStore struct {
	DB *sql.DB
}

// ListVisible returns accounts the parent hasn't hidden, for the kiosk
// balances panel.
func (s *AccountStore) ListVisible(ctx context.Context) ([]Account, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, simplefin_id, org_name, name, display_name, currency, balance_cents,
		       available_balance_cents, balance_date, visible, last_synced_at,
		       last_sync_error, created_at
		FROM bt_accounts WHERE visible = TRUE ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAccounts(rows)
}

// ListAll returns every account, for the settings page's visibility toggles.
func (s *AccountStore) ListAll(ctx context.Context) ([]Account, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, simplefin_id, org_name, name, display_name, currency, balance_cents,
		       available_balance_cents, balance_date, visible, last_synced_at,
		       last_sync_error, created_at
		FROM bt_accounts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAccounts(rows)
}

func scanAccounts(rows *sql.Rows) ([]Account, error) {
	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.SimpleFinID, &a.OrgName, &a.Name, &a.DisplayName, &a.Currency, &a.BalanceCents,
			&a.AvailableBalanceCents, &a.BalanceDate, &a.Visible, &a.LastSyncedAt, &a.LastSyncError, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Upsert creates or updates an account by its SimpleFIN id, called once per
// account on every scheduler refresh tick.
func (s *AccountStore) Upsert(ctx context.Context, a Account) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO bt_accounts
			(simplefin_id, org_name, name, currency, balance_cents, available_balance_cents,
			 balance_date, last_synced_at, last_sync_error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now(), NULL)
		ON CONFLICT (simplefin_id) DO UPDATE SET
			org_name = EXCLUDED.org_name,
			name = EXCLUDED.name,
			currency = EXCLUDED.currency,
			balance_cents = EXCLUDED.balance_cents,
			available_balance_cents = EXCLUDED.available_balance_cents,
			balance_date = EXCLUDED.balance_date,
			last_synced_at = now(),
			last_sync_error = NULL`,
		a.SimpleFinID, a.OrgName, a.Name, a.Currency, a.BalanceCents, a.AvailableBalanceCents, a.BalanceDate)
	return err
}

// SetVisible toggles whether an account shows on the kiosk balances panel.
func (s *AccountStore) SetVisible(ctx context.Context, id int, visible bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE bt_accounts SET visible = $2 WHERE id = $1`, id, visible)
	return err
}

// SetDisplayName sets a parent-chosen label for an account, shown instead of
// the raw SimpleFIN-reported name. An empty string clears it back to "use
// name" (stored as NULL, not "").
func (s *AccountStore) SetDisplayName(ctx context.Context, id int, displayName string) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE bt_accounts SET display_name = NULLIF($2, '') WHERE id = $1`,
		id, displayName)
	return err
}

// DeleteAll removes every synced account - called when SimpleFIN is
// disconnected.
func (s *AccountStore) DeleteAll(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM bt_accounts`)
	return err
}
