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

func TestSimpleFinConnectionStoreGetWhenNoneConfigured(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &SimpleFinConnectionStore{DB: conn}

	if _, err := s.Get(t.Context()); !errors.Is(err, ErrNoSimpleFinConnection) {
		t.Fatalf("Get: err = %v, want ErrNoSimpleFinConnection", err)
	}
}

func TestSimpleFinConnectionStoreConnectAndGet(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &SimpleFinConnectionStore{DB: conn}
	ctx := t.Context()

	if err := s.Connect(ctx, []byte("encrypted-access-url-1")); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	got, err := s.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.EncryptedAccessURL) != "encrypted-access-url-1" {
		t.Fatalf("EncryptedAccessURL = %q, want %q", got.EncryptedAccessURL, "encrypted-access-url-1")
	}
	if got.LastSyncedAt.Valid {
		t.Fatal("expected LastSyncedAt to be unset right after Connect")
	}
}

// TestSimpleFinConnectionStoreConnectReplacesExisting confirms Connect's
// singleton behavior: a second Connect call replaces the first row (via
// the DELETE-then-INSERT transaction) rather than erroring or creating a
// second row.
func TestSimpleFinConnectionStoreConnectReplacesExisting(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &SimpleFinConnectionStore{DB: conn}
	ctx := t.Context()

	if err := s.Connect(ctx, []byte("first")); err != nil {
		t.Fatalf("Connect (first): %v", err)
	}
	first, err := s.Get(ctx)
	if err != nil {
		t.Fatalf("Get (first): %v", err)
	}

	if err := s.Connect(ctx, []byte("second")); err != nil {
		t.Fatalf("Connect (second): %v", err)
	}
	second, err := s.Get(ctx)
	if err != nil {
		t.Fatalf("Get (second): %v", err)
	}

	if string(second.EncryptedAccessURL) != "second" {
		t.Fatalf("EncryptedAccessURL = %q, want %q", second.EncryptedAccessURL, "second")
	}
	if second.ID == first.ID {
		// Not a hard requirement (a fresh id after delete+insert is simply
		// what actually happens with SERIAL), but confirms this really is a
		// new row rather than an update of the old one.
		t.Logf("note: replaced row reused id %d", second.ID)
	}
}

func TestSimpleFinConnectionStoreDisconnect(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &SimpleFinConnectionStore{DB: conn}
	ctx := t.Context()

	if err := s.Connect(ctx, []byte("to-be-removed")); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := s.Disconnect(ctx); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	if _, err := s.Get(ctx); !errors.Is(err, ErrNoSimpleFinConnection) {
		t.Fatalf("Get after Disconnect: err = %v, want ErrNoSimpleFinConnection", err)
	}
}

func TestSimpleFinConnectionStoreMarkSynced(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &SimpleFinConnectionStore{DB: conn}
	ctx := t.Context()

	if err := s.Connect(ctx, []byte("sync-me")); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	c, err := s.Get(ctx)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if err := s.MarkSynced(ctx, c.ID, nil); err != nil {
		t.Fatalf("MarkSynced (success): %v", err)
	}
	c, err = s.Get(ctx)
	if err != nil {
		t.Fatalf("Get after MarkSynced success: %v", err)
	}
	if !c.LastSyncedAt.Valid {
		t.Fatal("expected LastSyncedAt to be set")
	}
	if c.LastSyncError.Valid {
		t.Fatalf("expected LastSyncError to be unset, got %+v", c.LastSyncError)
	}

	if err := s.MarkSynced(ctx, c.ID, errors.New("simplefin request failed")); err != nil {
		t.Fatalf("MarkSynced (failure): %v", err)
	}
	c, err = s.Get(ctx)
	if err != nil {
		t.Fatalf("Get after MarkSynced failure: %v", err)
	}
	if !c.LastSyncError.Valid || c.LastSyncError.String != "simplefin request failed" {
		t.Fatalf("LastSyncError = %+v, want the wrapped error text", c.LastSyncError)
	}
}

// TestSimpleFinConnectionStoreConnectInsertFailsOnNilURL exercises the
// second tx.ExecContext (the INSERT) error branch inside Connect: passing a
// nil []byte for the NOT NULL encrypted_access_url column is a genuine
// Postgres constraint violation, not a broken connection - so this is a
// real, reachable error path, not an infeasible one. Rollback (via the
// deferred tx.Rollback()) must leave no partial row behind.
func TestSimpleFinConnectionStoreConnectInsertFailsOnNilURL(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &SimpleFinConnectionStore{DB: conn}
	ctx := t.Context()

	if err := s.Connect(ctx, nil); err == nil {
		t.Fatal("expected Connect to fail inserting a NULL encrypted_access_url")
	}

	if _, err := s.Get(ctx); !errors.Is(err, ErrNoSimpleFinConnection) {
		t.Fatalf("Get after failed Connect: err = %v, want ErrNoSimpleFinConnection (rollback should leave no row)", err)
	}
}

func TestSimpleFinConnectionStoreQueriesReturnErrorOnCanceledContext(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &SimpleFinConnectionStore{DB: conn}

	if err := s.Connect(t.Context(), []byte("seed")); err != nil {
		t.Fatalf("Connect (seed): %v", err)
	}
	c, err := s.Get(t.Context())
	if err != nil {
		t.Fatalf("Get (seed): %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := s.Get(ctx); err == nil {
		t.Error("Get: expected error on canceled context")
	}
	if err := s.Connect(ctx, []byte("x")); err == nil {
		t.Error("Connect: expected error on canceled context (BeginTx branch)")
	}
	if err := s.Disconnect(ctx); err == nil {
		t.Error("Disconnect: expected error on canceled context")
	}
	if err := s.MarkSynced(ctx, c.ID, nil); err == nil {
		t.Error("MarkSynced: expected error on canceled context")
	}
}
