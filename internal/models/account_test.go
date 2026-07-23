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
	"testing"
	"time"

	"github.com/mscreations/billtracker-plugin/internal/testutil"
)

func TestAccountEffectiveName(t *testing.T) {
	cases := []struct {
		name string
		a    Account
		want string
	}{
		{"display name set", Account{Name: "Checking ...1234", DisplayName: sql.NullString{String: "Everyday Checking", Valid: true}}, "Everyday Checking"},
		{"display name empty string but valid", Account{Name: "Checking ...1234", DisplayName: sql.NullString{String: "", Valid: true}}, "Checking ...1234"},
		{"display name not set", Account{Name: "Checking ...1234"}, "Checking ...1234"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.EffectiveName(); got != tc.want {
				t.Errorf("EffectiveName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAccountIsCredit(t *testing.T) {
	if (Account{BalanceCents: 100}).IsCredit() {
		t.Error("positive balance should not be credit")
	}
	if !(Account{BalanceCents: -100}).IsCredit() {
		t.Error("negative balance should be credit")
	}
	if (Account{BalanceCents: 0}).IsCredit() {
		t.Error("zero balance should not be credit")
	}
}

func TestAccountStoreUpsertCreatesAndUpdates(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &AccountStore{DB: conn}
	ctx := t.Context()

	a := Account{
		SimpleFinID:           "acct-1",
		OrgName:               sql.NullString{String: "Example Bank", Valid: true},
		Name:                  "Checking",
		Currency:              "USD",
		BalanceCents:          12345,
		AvailableBalanceCents: sql.NullInt64{Int64: 12000, Valid: true},
		BalanceDate:           sql.NullTime{Time: time.Now(), Valid: true},
	}
	if err := s.Upsert(ctx, a); err != nil {
		t.Fatalf("Upsert (create): %v", err)
	}

	all, err := s.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ListAll returned %d accounts, want 1", len(all))
	}
	got := all[0]
	if got.Name != "Checking" || got.BalanceCents != 12345 {
		t.Fatalf("unexpected account: %+v", got)
	}
	if !got.Visible {
		t.Fatal("newly created account should default visible=true")
	}
	if !got.LastSyncedAt.Valid {
		t.Fatal("expected LastSyncedAt to be set by Upsert")
	}

	// Update via a second Upsert with the same SimpleFinID.
	a.Name = "Checking Renamed"
	a.BalanceCents = 999
	if err := s.Upsert(ctx, a); err != nil {
		t.Fatalf("Upsert (update): %v", err)
	}
	all, err = s.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll after update: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected upsert to update in place, got %d rows", len(all))
	}
	if all[0].Name != "Checking Renamed" || all[0].BalanceCents != 999 {
		t.Fatalf("unexpected account after update: %+v", all[0])
	}
}

func TestAccountStoreListVisibleFiltersHidden(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &AccountStore{DB: conn}
	ctx := t.Context()

	if err := s.Upsert(ctx, Account{SimpleFinID: "visible-1", Name: "Visible", Currency: "USD", BalanceCents: 1}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.Upsert(ctx, Account{SimpleFinID: "hidden-1", Name: "Hidden", Currency: "USD", BalanceCents: 1}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	all, err := s.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	var hiddenID int
	for _, a := range all {
		if a.SimpleFinID == "hidden-1" {
			hiddenID = a.ID
		}
	}
	if hiddenID == 0 {
		t.Fatal("could not find hidden-1 account id")
	}
	if err := s.SetVisible(ctx, hiddenID, false); err != nil {
		t.Fatalf("SetVisible: %v", err)
	}

	visible, err := s.ListVisible(ctx)
	if err != nil {
		t.Fatalf("ListVisible: %v", err)
	}
	if len(visible) != 1 || visible[0].SimpleFinID != "visible-1" {
		t.Fatalf("ListVisible = %+v, want only visible-1", visible)
	}
}

func TestAccountStoreSetDisplayName(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &AccountStore{DB: conn}
	ctx := t.Context()

	if err := s.Upsert(ctx, Account{SimpleFinID: "dn-1", Name: "Raw Name", Currency: "USD", BalanceCents: 1}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	all, err := s.ListAll(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("ListAll: %v (%d rows)", err, len(all))
	}
	id := all[0].ID

	if err := s.SetDisplayName(ctx, id, "Friendly Name"); err != nil {
		t.Fatalf("SetDisplayName: %v", err)
	}
	all, _ = s.ListAll(ctx)
	if all[0].EffectiveName() != "Friendly Name" {
		t.Fatalf("EffectiveName() = %q, want Friendly Name", all[0].EffectiveName())
	}

	// Clearing back to "" should NULL it out (falls back to raw name).
	if err := s.SetDisplayName(ctx, id, ""); err != nil {
		t.Fatalf("SetDisplayName (clear): %v", err)
	}
	all, _ = s.ListAll(ctx)
	if all[0].DisplayName.Valid {
		t.Fatalf("expected DisplayName to be NULL after clearing, got %+v", all[0].DisplayName)
	}
	if all[0].EffectiveName() != "Raw Name" {
		t.Fatalf("EffectiveName() = %q, want Raw Name", all[0].EffectiveName())
	}
}

func TestAccountStoreDeleteAll(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &AccountStore{DB: conn}
	ctx := t.Context()

	if err := s.Upsert(ctx, Account{SimpleFinID: "del-1", Name: "A", Currency: "USD", BalanceCents: 1}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.Upsert(ctx, Account{SimpleFinID: "del-2", Name: "B", Currency: "USD", BalanceCents: 1}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := s.DeleteAll(ctx); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	all, err := s.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("expected 0 accounts after DeleteAll, got %d", len(all))
	}
}

// TestAccountStoreQueriesReturnErrorOnCanceledContext covers the
// QueryContext/ExecContext error-return branches of every AccountStore
// method, otherwise unreachable without breaking the DB connection itself.
func TestAccountStoreQueriesReturnErrorOnCanceledContext(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &AccountStore{DB: conn}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := s.ListVisible(ctx); err == nil {
		t.Error("ListVisible: expected error on canceled context")
	}
	if _, err := s.ListAll(ctx); err == nil {
		t.Error("ListAll: expected error on canceled context")
	}
	if err := s.Upsert(ctx, Account{SimpleFinID: "x", Name: "x", Currency: "USD"}); err == nil {
		t.Error("Upsert: expected error on canceled context")
	}
	if err := s.SetVisible(ctx, 1, true); err == nil {
		t.Error("SetVisible: expected error on canceled context")
	}
	if err := s.SetDisplayName(ctx, 1, "x"); err == nil {
		t.Error("SetDisplayName: expected error on canceled context")
	}
	if err := s.DeleteAll(ctx); err == nil {
		t.Error("DeleteAll: expected error on canceled context")
	}
}
