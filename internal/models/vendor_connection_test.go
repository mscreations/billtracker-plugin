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
	"errors"
	"testing"

	"github.com/mscreations/billtracker-plugin/internal/testutil"
)

func createBillDefForVendorTest(t *testing.T, defs *BillDefinitionStore, name string) int {
	t.Helper()
	id, err := defs.Create(t.Context(), BillDefinition{
		Name:         name,
		AmountCents:  1000,
		ScheduleType: ScheduleVendor,
	})
	if err != nil {
		t.Fatalf("Create bill definition: %v", err)
	}
	return id
}

func TestVendorConnectionStoreUpsertCreatesAndUpdates(t *testing.T) {
	conn := testutil.RequireDB(t)
	defs := &BillDefinitionStore{DB: conn}
	s := &VendorConnectionStore{DB: conn}
	ctx := t.Context()

	billID := createBillDefForVendorTest(t, defs, "WV Water Authority")

	id, err := s.Upsert(ctx, VendorConnection{
		BillDefinitionID:  billID,
		Connector:         "billeriq",
		Tenant:            "WVWAuthority",
		Username:          "parent@example.com",
		EncryptedPassword: []byte("encrypted-pw-1"),
	})
	if err != nil {
		t.Fatalf("Upsert (create): %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	got, err := s.GetByBillDefinitionID(ctx, billID)
	if err != nil {
		t.Fatalf("GetByBillDefinitionID: %v", err)
	}
	if got.Connector != "billeriq" || got.Tenant != "WVWAuthority" || got.Username != "parent@example.com" {
		t.Fatalf("unexpected connection: %+v", got)
	}
	if got.BootstrapManaged {
		t.Fatal("expected BootstrapManaged=false")
	}

	// Upsert again for the same bill definition should update in place.
	id2, err := s.Upsert(ctx, VendorConnection{
		BillDefinitionID:  billID,
		Connector:         "columbiagaspa",
		Tenant:            "",
		Username:          "new-user@example.com",
		EncryptedPassword: []byte("encrypted-pw-2"),
		BootstrapManaged:  true,
	})
	if err != nil {
		t.Fatalf("Upsert (update): %v", err)
	}
	if id2 != id {
		t.Fatalf("Upsert on conflict returned a different id: got %d, want %d", id2, id)
	}

	got, err = s.GetByBillDefinitionID(ctx, billID)
	if err != nil {
		t.Fatalf("GetByBillDefinitionID after update: %v", err)
	}
	if got.Connector != "columbiagaspa" || got.Username != "new-user@example.com" || !got.BootstrapManaged {
		t.Fatalf("unexpected connection after update: %+v", got)
	}

	all, err := s.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ListAll = %d rows, want 1 (upsert should not duplicate)", len(all))
	}
}

func TestVendorConnectionStoreGetByBillDefinitionIDNotFound(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &VendorConnectionStore{DB: conn}
	if _, err := s.GetByBillDefinitionID(t.Context(), 999999); !errors.Is(err, ErrVendorConnectionNotFound) {
		t.Fatalf("GetByBillDefinitionID: err = %v, want ErrVendorConnectionNotFound", err)
	}
}

func TestVendorConnectionStoreDelete(t *testing.T) {
	conn := testutil.RequireDB(t)
	defs := &BillDefinitionStore{DB: conn}
	s := &VendorConnectionStore{DB: conn}
	ctx := t.Context()

	billID := createBillDefForVendorTest(t, defs, "Delete Me Utility")
	id, err := s.Upsert(ctx, VendorConnection{
		BillDefinitionID:  billID,
		Connector:         "billeriq",
		Tenant:            "t",
		Username:          "u",
		EncryptedPassword: []byte("pw"),
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := s.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.GetByBillDefinitionID(ctx, billID); !errors.Is(err, ErrVendorConnectionNotFound) {
		t.Fatalf("GetByBillDefinitionID after delete: err = %v, want ErrVendorConnectionNotFound", err)
	}
}

func TestVendorConnectionStoreMarkSynced(t *testing.T) {
	conn := testutil.RequireDB(t)
	defs := &BillDefinitionStore{DB: conn}
	s := &VendorConnectionStore{DB: conn}
	ctx := t.Context()

	billID := createBillDefForVendorTest(t, defs, "Sync Utility")
	id, err := s.Upsert(ctx, VendorConnection{
		BillDefinitionID:  billID,
		Connector:         "billeriq",
		Tenant:            "t",
		Username:          "u",
		EncryptedPassword: []byte("pw"),
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Success path: sets last_account_number since a non-empty string is
	// passed, and clears any prior error.
	if err := s.MarkSynced(ctx, id, "ACCT-123", nil); err != nil {
		t.Fatalf("MarkSynced (success): %v", err)
	}
	got, err := s.GetByBillDefinitionID(ctx, billID)
	if err != nil {
		t.Fatalf("GetByBillDefinitionID: %v", err)
	}
	if !got.LastSyncedAt.Valid {
		t.Fatal("expected LastSyncedAt to be set")
	}
	if !got.LastAccountNumber.Valid || got.LastAccountNumber.String != "ACCT-123" {
		t.Fatalf("LastAccountNumber = %+v, want ACCT-123", got.LastAccountNumber)
	}
	if got.LastSyncError.Valid {
		t.Fatalf("expected LastSyncError unset, got %+v", got.LastSyncError)
	}

	// Failure path with an empty account number: COALESCE keeps the
	// previously-known account number rather than clearing it, and
	// last_sync_error gets set.
	if err := s.MarkSynced(ctx, id, "", errors.New("login failed")); err != nil {
		t.Fatalf("MarkSynced (failure): %v", err)
	}
	got, err = s.GetByBillDefinitionID(ctx, billID)
	if err != nil {
		t.Fatalf("GetByBillDefinitionID: %v", err)
	}
	if !got.LastAccountNumber.Valid || got.LastAccountNumber.String != "ACCT-123" {
		t.Fatalf("LastAccountNumber after failed sync = %+v, want it preserved as ACCT-123", got.LastAccountNumber)
	}
	if !got.LastSyncError.Valid || got.LastSyncError.String != "login failed" {
		t.Fatalf("LastSyncError = %+v, want 'login failed'", got.LastSyncError)
	}
}

func TestVendorConnectionStoreQueriesReturnErrorOnCanceledContext(t *testing.T) {
	conn := testutil.RequireDB(t)
	defs := &BillDefinitionStore{DB: conn}
	s := &VendorConnectionStore{DB: conn}

	billID := createBillDefForVendorTest(t, defs, "Cancel Ctx Utility")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := s.ListAll(ctx); err == nil {
		t.Error("ListAll: expected error on canceled context")
	}
	if _, err := s.GetByBillDefinitionID(ctx, billID); err == nil {
		t.Error("GetByBillDefinitionID: expected error on canceled context")
	}
	if _, err := s.Upsert(ctx, VendorConnection{BillDefinitionID: billID, Connector: "x", Tenant: "x", Username: "x", EncryptedPassword: []byte("x")}); err == nil {
		t.Error("Upsert: expected error on canceled context")
	}
	if err := s.Delete(ctx, 1); err == nil {
		t.Error("Delete: expected error on canceled context")
	}
	if err := s.MarkSynced(ctx, 1, "x", nil); err == nil {
		t.Error("MarkSynced: expected error on canceled context")
	}
}
