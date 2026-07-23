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

package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mscreations/billtracker-plugin/internal/models"
)

func TestFormatCentsAndPlain(t *testing.T) {
	if got := formatCents(12345); got != "$123.45" {
		t.Errorf("formatCents(12345) = %q, want $123.45", got)
	}
	if got := formatCentsPlain(12345); got != "123.45" {
		t.Errorf("formatCentsPlain(12345) = %q, want 123.45", got)
	}
}

func TestViewRendersUnpaidBillsWithoutSimpleFin(t *testing.T) {
	a := newFullTestApp(t)
	createTestBill(t, a, "Internet", time.Now().AddDate(0, 0, -2)) // overdue

	req := httptest.NewRequest(http.MethodGet, "/view", nil)
	rec := httptest.NewRecorder()
	a.View(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Internet") {
		t.Errorf("expected the bill name in the rendered page, got: %s", body)
	}
}

func TestViewRendersAccountsWhenSimpleFinConnected(t *testing.T) {
	a := newFullTestApp(t)
	ctx := t.Context()

	encrypted, err := a.Encryptor.Encrypt("https://simplefin.example/access")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := a.SimpleFin.Connect(ctx, encrypted); err != nil {
		t.Fatalf("SimpleFin.Connect: %v", err)
	}
	if err := a.Accounts.Upsert(ctx, models.Account{
		SimpleFinID:  "acc-1",
		Name:         "Checking",
		Currency:     "USD",
		BalanceCents: 5000,
	}); err != nil {
		t.Fatalf("Accounts.Upsert: %v", err)
	}
	if err := a.Accounts.Upsert(ctx, models.Account{
		SimpleFinID:  "acc-2",
		Name:         "Credit Card",
		Currency:     "USD",
		BalanceCents: -2000,
	}); err != nil {
		t.Fatalf("Accounts.Upsert (credit): %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/view", nil)
	rec := httptest.NewRecorder()
	a.View(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Checking") || !strings.Contains(body, "Credit Card") {
		t.Errorf("expected both accounts in the rendered page, got: %s", body)
	}
}

func TestViewReturnsServerErrorOnListUpcomingUnpaidFailure(t *testing.T) {
	a := newFullTestApp(t)
	a.Instances = &models.BillInstanceStore{DB: brokenBTDB(t)}

	req := httptest.NewRequest(http.MethodGet, "/view", nil)
	rec := httptest.NewRecorder()
	a.View(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
