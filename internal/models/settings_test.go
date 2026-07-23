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
	"testing"

	"github.com/mscreations/billtracker-plugin/internal/testutil"
)

func TestSettingsStoreGetFallbackWhenMissing(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &SettingsStore{DB: conn}

	got, err := s.Get(t.Context(), "does_not_exist", "fallback-value")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "fallback-value" {
		t.Fatalf("Get() = %q, want fallback", got)
	}
}

func TestSettingsStoreSetAndGet(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &SettingsStore{DB: conn}
	ctx := t.Context()

	if err := s.Set(ctx, "app_title", "Bill Tracker"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get(ctx, "app_title", "fallback")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "Bill Tracker" {
		t.Fatalf("Get() = %q, want %q", got, "Bill Tracker")
	}
}

func TestSettingsStoreSetUpsertsExistingKey(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &SettingsStore{DB: conn}
	ctx := t.Context()

	if err := s.Set(ctx, "plugin_token", "token-1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set(ctx, "plugin_token", "token-2"); err != nil {
		t.Fatalf("Set (overwrite): %v", err)
	}

	got, err := s.Get(ctx, "plugin_token", "fallback")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "token-2" {
		t.Fatalf("Get() = %q, want updated value %q", got, "token-2")
	}
}

func TestSettingsStoreSetIfAbsent(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &SettingsStore{DB: conn}
	ctx := t.Context()

	won, err := s.SetIfAbsent(ctx, "plugin_token", "first")
	if err != nil {
		t.Fatalf("SetIfAbsent (first): %v", err)
	}
	if !won {
		t.Fatal("expected the first SetIfAbsent call to win")
	}

	won, err = s.SetIfAbsent(ctx, "plugin_token", "second")
	if err != nil {
		t.Fatalf("SetIfAbsent (second): %v", err)
	}
	if won {
		t.Fatal("expected the second SetIfAbsent call to lose")
	}

	got, err := s.Get(ctx, "plugin_token", "fallback")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "first" {
		t.Fatalf("Get() = %q, want the winning value %q (loser must not overwrite)", got, "first")
	}
}

func TestSettingsStoreQueriesReturnErrorOnCanceledContext(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &SettingsStore{DB: conn}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := s.Get(ctx, "any", "fallback"); err == nil {
		t.Error("Get: expected error on canceled context")
	}
	if err := s.Set(ctx, "any", "value"); err == nil {
		t.Error("Set: expected error on canceled context")
	}
	if _, err := s.SetIfAbsent(ctx, "any", "value"); err == nil {
		t.Error("SetIfAbsent: expected error on canceled context")
	}
}
