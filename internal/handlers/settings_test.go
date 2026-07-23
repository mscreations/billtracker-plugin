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
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mscreations/billtracker-plugin/internal/models"
	"github.com/mscreations/billtracker-plugin/internal/simplefin"
)

func TestSettingsPageGETRendersEmptyState(t *testing.T) {
	a := newFullTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rec := httptest.NewRecorder()
	a.SettingsPage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
}

func TestSettingsPageRejectsMalformedForm(t *testing.T) {
	a := newFullTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/settings?%zz", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.SettingsPage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (error rendered in-page)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "bad form") {
		t.Errorf("expected a bad-form error message, got: %s", rec.Body.String())
	}
}

func TestSettingsPageUnknownAction(t *testing.T) {
	a := newFullTestApp(t)
	req := postForm(t, "/settings", url.Values{"action": {"nonexistent"}})
	rec := httptest.NewRecorder()
	a.SettingsPage(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "unknown action") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSettingsPageReturnsServerErrorWhenBuildDataFails(t *testing.T) {
	a := newFullTestApp(t)
	a.BillDefs = &models.BillDefinitionStore{DB: brokenBTDB(t)}
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rec := httptest.NewRecorder()
	a.SettingsPage(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func postForm(t *testing.T, path string, vals url.Values) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// --- add_bill / update_bill / delete_bill ---

func TestAddBillMonthly(t *testing.T) {
	a := newFullTestApp(t)
	req := postForm(t, "/settings", url.Values{
		"action": {"add_bill"}, "name": {"Rent"}, "amount": {"1500"},
		"schedule": {"monthly"}, "day_of_month": {"1"},
	})
	rec := httptest.NewRecorder()
	a.SettingsPage(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Added") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	defs, err := a.BillDefs.ListAll(t.Context())
	if err != nil || len(defs) != 1 {
		t.Fatalf("ListAll: defs=%+v err=%v", defs, err)
	}
}

func TestAddBillQuarterly(t *testing.T) {
	a := newFullTestApp(t)
	req := postForm(t, "/settings", url.Values{
		"action": {"add_bill"}, "name": {"Insurance"}, "amount": {"300"},
		"schedule": {"quarterly"}, "day_of_month": {"15"}, "quarter_start_month": {"2"},
	})
	rec := httptest.NewRecorder()
	a.SettingsPage(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Added") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAddBillOneOff(t *testing.T) {
	a := newFullTestApp(t)
	req := postForm(t, "/settings", url.Values{
		"action": {"add_bill"}, "name": {"Property Tax"}, "amount": {"2200"},
		"schedule": {"one_off"}, "one_off_date": {"2026-12-01"},
	})
	rec := httptest.NewRecorder()
	a.SettingsPage(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Added") {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestAddBillValidationErrors(t *testing.T) {
	a := newFullTestApp(t)
	for _, tc := range []struct {
		name string
		vals url.Values
		want string
	}{
		{"missing name", url.Values{"action": {"add_bill"}, "amount": {"10"}, "schedule": {"monthly"}, "day_of_month": {"1"}}, "name is required"},
		{"invalid amount", url.Values{"action": {"add_bill"}, "name": {"X"}, "amount": {"nope"}, "schedule": {"monthly"}, "day_of_month": {"1"}}, "invalid amount"},
		{"invalid schedule", url.Values{"action": {"add_bill"}, "name": {"X"}, "amount": {"10"}, "schedule": {"weekly"}}, "schedule must be"},
		{"monthly bad day", url.Values{"action": {"add_bill"}, "name": {"X"}, "amount": {"10"}, "schedule": {"monthly"}, "day_of_month": {"99"}}, "day_of_month must be between"},
		{"monthly missing day", url.Values{"action": {"add_bill"}, "name": {"X"}, "amount": {"10"}, "schedule": {"monthly"}}, "day_of_month must be between"},
		{"quarterly bad day", url.Values{"action": {"add_bill"}, "name": {"X"}, "amount": {"10"}, "schedule": {"quarterly"}, "day_of_month": {"99"}, "quarter_start_month": {"1"}}, "day_of_month must be between"},
		{"quarterly bad start month", url.Values{"action": {"add_bill"}, "name": {"X"}, "amount": {"10"}, "schedule": {"quarterly"}, "day_of_month": {"1"}, "quarter_start_month": {"9"}}, "quarter_start_month must be"},
		{"one_off bad date", url.Values{"action": {"add_bill"}, "name": {"X"}, "amount": {"10"}, "schedule": {"one_off"}, "one_off_date": {"not-a-date"}}, "one_off_date must be"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := postForm(t, "/settings", tc.vals)
			rec := httptest.NewRecorder()
			a.SettingsPage(rec, req)
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("status = %d, body = %s, want it to contain %q", rec.Code, rec.Body.String(), tc.want)
			}
		})
	}
}

// TestAddBillCreateFailureReturnsError covers addBill's Create-error branch
// via a real, connection-healthy Postgres error (amount_cents overflowing
// the column's INTEGER range) rather than a broken store - Create and the
// subsequent buildSettingsPageData's ListAll/GetByID calls all share
// a.BillDefs, so breaking the whole store would also fail the page render
// itself instead of exercising this specific error message.
func TestAddBillCreateFailureReturnsError(t *testing.T) {
	a := newFullTestApp(t)
	req := postForm(t, "/settings", url.Values{
		"action": {"add_bill"}, "name": {"X"}, "amount": {"99999999999"}, "schedule": {"monthly"}, "day_of_month": {"1"},
	})
	rec := httptest.NewRecorder()
	a.SettingsPage(rec, req)
	if !strings.Contains(rec.Body.String(), "creating bill") {
		t.Fatalf("body = %s, want a creating-bill error", rec.Body.String())
	}
}

func TestUpdateBillHappyPathAndValidation(t *testing.T) {
	a := newFullTestApp(t)
	id, err := a.BillDefs.Create(t.Context(), models.BillDefinition{Name: "Old", AmountCents: 100, ScheduleType: models.ScheduleMonthly, DayOfMonth: sql.NullInt16{Int16: 1, Valid: true}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Run("invalid id", func(t *testing.T) {
		req := postForm(t, "/settings", url.Values{"action": {"update_bill"}, "id": {"not-a-number"}})
		rec := httptest.NewRecorder()
		a.SettingsPage(rec, req)
		if !strings.Contains(rec.Body.String(), "invalid bill id") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		req := postForm(t, "/settings", url.Values{"action": {"update_bill"}, "id": {"999999"}})
		rec := httptest.NewRecorder()
		a.SettingsPage(rec, req)
		if !strings.Contains(rec.Body.String(), "not found") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("success", func(t *testing.T) {
		req := postForm(t, "/settings", url.Values{
			"action": {"update_bill"}, "id": {strconv.Itoa(id)}, "name": {"New Name"}, "amount": {"250"},
			"schedule": {"monthly"}, "day_of_month": {"5"},
		})
		rec := httptest.NewRecorder()
		a.SettingsPage(rec, req)
		if !strings.Contains(rec.Body.String(), "Updated") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("bootstrap managed rejected", func(t *testing.T) {
		bsID, err := a.BillDefs.Create(t.Context(), models.BillDefinition{Name: "BS", AmountCents: 100, ScheduleType: models.ScheduleMonthly, DayOfMonth: sql.NullInt16{Int16: 1, Valid: true}, BootstrapManaged: true})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		req := postForm(t, "/settings", url.Values{
			"action": {"update_bill"}, "id": {strconv.Itoa(bsID)}, "name": {"X"}, "amount": {"10"}, "schedule": {"monthly"}, "day_of_month": {"1"},
		})
		rec := httptest.NewRecorder()
		a.SettingsPage(rec, req)
		if !strings.Contains(rec.Body.String(), "managed by bills.json") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("invalid form after lookup", func(t *testing.T) {
		id2, err := a.BillDefs.Create(t.Context(), models.BillDefinition{Name: "Another", AmountCents: 100, ScheduleType: models.ScheduleMonthly, DayOfMonth: sql.NullInt16{Int16: 1, Valid: true}})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		req := postForm(t, "/settings", url.Values{"action": {"update_bill"}, "id": {strconv.Itoa(id2)}, "name": {""}, "amount": {"10"}, "schedule": {"monthly"}, "day_of_month": {"1"}})
		rec := httptest.NewRecorder()
		a.SettingsPage(rec, req)
		if !strings.Contains(rec.Body.String(), "name is required") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})
}

func TestDeleteBillValidationAndSuccess(t *testing.T) {
	a := newFullTestApp(t)

	t.Run("invalid id", func(t *testing.T) {
		req := postForm(t, "/settings", url.Values{"action": {"delete_bill"}, "id": {"nope"}})
		rec := httptest.NewRecorder()
		a.SettingsPage(rec, req)
		if !strings.Contains(rec.Body.String(), "invalid bill id") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		req := postForm(t, "/settings", url.Values{"action": {"delete_bill"}, "id": {"999999"}})
		rec := httptest.NewRecorder()
		a.SettingsPage(rec, req)
		if !strings.Contains(rec.Body.String(), "not found") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("bootstrap managed rejected", func(t *testing.T) {
		id, err := a.BillDefs.Create(t.Context(), models.BillDefinition{Name: "BS2", AmountCents: 100, ScheduleType: models.ScheduleMonthly, DayOfMonth: sql.NullInt16{Int16: 1, Valid: true}, BootstrapManaged: true})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		req := postForm(t, "/settings", url.Values{"action": {"delete_bill"}, "id": {strconv.Itoa(id)}})
		rec := httptest.NewRecorder()
		a.SettingsPage(rec, req)
		if !strings.Contains(rec.Body.String(), "managed by bills.json") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("success", func(t *testing.T) {
		id, err := a.BillDefs.Create(t.Context(), models.BillDefinition{Name: "Deletable", AmountCents: 100, ScheduleType: models.ScheduleMonthly, DayOfMonth: sql.NullInt16{Int16: 1, Valid: true}})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		req := postForm(t, "/settings", url.Values{"action": {"delete_bill"}, "id": {strconv.Itoa(id)}})
		rec := httptest.NewRecorder()
		a.SettingsPage(rec, req)
		if !strings.Contains(rec.Body.String(), "Deleted") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})
}

func TestMarkPaidValidationAndSuccess(t *testing.T) {
	a := newFullTestApp(t)

	t.Run("invalid id", func(t *testing.T) {
		req := postForm(t, "/settings", url.Values{"action": {"mark_paid"}, "id": {"nope"}})
		rec := httptest.NewRecorder()
		a.SettingsPage(rec, req)
		if !strings.Contains(rec.Body.String(), "invalid instance id") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		req := postForm(t, "/settings", url.Values{"action": {"mark_paid"}, "id": {"999999"}})
		rec := httptest.NewRecorder()
		a.SettingsPage(rec, req)
		if !strings.Contains(rec.Body.String(), "marking paid") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("success", func(t *testing.T) {
		_, instID := createTestBill(t, a, "PayMe", time.Now().AddDate(0, 0, 1))
		req := postForm(t, "/settings", url.Values{"action": {"mark_paid"}, "id": {strconv.Itoa(instID)}})
		rec := httptest.NewRecorder()
		a.SettingsPage(rec, req)
		if !strings.Contains(rec.Body.String(), "Marked paid") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})
}

// --- SimpleFIN actions ---

func TestConnectSimpleFinRequiresToken(t *testing.T) {
	a := newFullTestApp(t)
	req := postForm(t, "/settings", url.Values{"action": {"connect_simplefin"}, "setup_token": {""}})
	rec := httptest.NewRecorder()
	a.SettingsPage(rec, req)
	if !strings.Contains(rec.Body.String(), "setup token is required") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestConnectSimpleFinRejectsInvalidToken(t *testing.T) {
	a := newFullTestApp(t)
	req := postForm(t, "/settings", url.Values{"action": {"connect_simplefin"}, "setup_token": {"not-valid-base64!!!"}})
	rec := httptest.NewRecorder()
	a.SettingsPage(rec, req)
	if !strings.Contains(rec.Body.String(), "connecting to SimpleFIN") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestConnectSimpleFinSuccess(t *testing.T) {
	simplefin.InsecureTestMode = true
	t.Cleanup(func() { simplefin.InsecureTestMode = false })
	a := newFullTestApp(t)
	claim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("https://user:pass@simplefin.example/access"))
	}))
	defer claim.Close()
	token := base64.StdEncoding.EncodeToString([]byte(claim.URL))

	req := postForm(t, "/settings", url.Values{"action": {"connect_simplefin"}, "setup_token": {token}})
	rec := httptest.NewRecorder()
	a.SettingsPage(rec, req)

	if !strings.Contains(rec.Body.String(), "SimpleFIN connected") {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if _, err := a.SimpleFin.Get(t.Context()); err != nil {
		t.Fatalf("expected a stored SimpleFIN connection: %v", err)
	}
}

// TestConnectSimpleFinSaveFailure covers connectSimpleFin's Connect-error
// branch, calling it directly rather than through the full SettingsPage
// handler: buildSettingsPageData (which SettingsPage always calls next)
// unconditionally calls a.SimpleFin.Get too, so breaking the whole store
// would trip that generic 500 branch instead of rendering this specific
// error message.
func TestConnectSimpleFinSaveFailure(t *testing.T) {
	simplefin.InsecureTestMode = true
	t.Cleanup(func() { simplefin.InsecureTestMode = false })
	a := newFullTestApp(t)
	claim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("https://user:pass@simplefin.example/access"))
	}))
	defer claim.Close()
	token := base64.StdEncoding.EncodeToString([]byte(claim.URL))

	a.SimpleFin = &models.SimpleFinConnectionStore{DB: brokenBTDB(t)}
	req := postForm(t, "/settings", url.Values{"setup_token": {token}})
	if err := req.ParseForm(); err != nil {
		t.Fatalf("ParseForm: %v", err)
	}
	errMsg, _ := a.connectSimpleFin(t.Context(), req)
	if !strings.Contains(errMsg, "saving SimpleFIN connection") {
		t.Fatalf("errMsg = %q", errMsg)
	}
}

func TestDisconnectSimpleFin(t *testing.T) {
	a := newFullTestApp(t)
	encrypted, err := a.Encryptor.Encrypt("https://x/access")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := a.SimpleFin.Connect(t.Context(), encrypted); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	req := postForm(t, "/settings", url.Values{"action": {"disconnect_simplefin"}})
	rec := httptest.NewRecorder()
	a.SettingsPage(rec, req)
	if !strings.Contains(rec.Body.String(), "disconnected") {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if _, err := a.SimpleFin.Get(t.Context()); err != models.ErrNoSimpleFinConnection {
		t.Fatalf("expected no connection after disconnect, got err=%v", err)
	}
}

func TestDisconnectSimpleFinDeleteAllAccountsFailure(t *testing.T) {
	a := newFullTestApp(t)
	encrypted, err := a.Encryptor.Encrypt("https://x/access")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := a.SimpleFin.Connect(t.Context(), encrypted); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	a.Accounts = &models.AccountStore{DB: brokenBTDB(t)}

	req := postForm(t, "/settings", url.Values{"action": {"disconnect_simplefin"}})
	rec := httptest.NewRecorder()
	a.SettingsPage(rec, req)
	if !strings.Contains(rec.Body.String(), "removing synced accounts") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRefreshSimpleFinNotConnected(t *testing.T) {
	a := newFullTestApp(t)
	req := postForm(t, "/settings", url.Values{"action": {"refresh_simplefin"}})
	rec := httptest.NewRecorder()
	a.SettingsPage(rec, req)
	if !strings.Contains(rec.Body.String(), "not connected") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRefreshSimpleFinConnected(t *testing.T) {
	a := newFullTestApp(t)
	encrypted, err := a.Encryptor.Encrypt("https://x/access")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := a.SimpleFin.Connect(t.Context(), encrypted); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	req := postForm(t, "/settings", url.Values{"action": {"refresh_simplefin"}})
	rec := httptest.NewRecorder()
	a.SettingsPage(rec, req)
	if !strings.Contains(rec.Body.String(), "Refreshing balances now") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

// --- account visibility/display name ---

func TestToggleAccountVisibility(t *testing.T) {
	a := newFullTestApp(t)
	ctx := t.Context()
	if err := a.Accounts.Upsert(ctx, models.Account{SimpleFinID: "acc-vis", Name: "Vis Test", Currency: "USD"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	accts, err := a.Accounts.ListAll(ctx)
	if err != nil || len(accts) != 1 {
		t.Fatalf("ListAll: %+v %v", accts, err)
	}

	t.Run("invalid id", func(t *testing.T) {
		req := postForm(t, "/settings", url.Values{"action": {"toggle_account_visibility"}, "id": {"nope"}})
		rec := httptest.NewRecorder()
		a.SettingsPage(rec, req)
		if !strings.Contains(rec.Body.String(), "invalid account id") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("success", func(t *testing.T) {
		req := postForm(t, "/settings", url.Values{"action": {"toggle_account_visibility"}, "id": {strconv.Itoa(accts[0].ID)}, "visible": {"false"}})
		rec := httptest.NewRecorder()
		a.SettingsPage(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		updated, err := a.Accounts.ListAll(ctx)
		if err != nil || updated[0].Visible {
			t.Fatalf("expected account to be hidden: %+v %v", updated, err)
		}
	})

	t.Run("store failure", func(t *testing.T) {
		broken := *a
		broken.Accounts = &models.AccountStore{DB: brokenBTDB(t)}
		req := postForm(t, "/settings", url.Values{"action": {"toggle_account_visibility"}, "id": {strconv.Itoa(accts[0].ID)}, "visible": {"true"}})
		rec := httptest.NewRecorder()
		broken.SettingsPage(rec, req)
		if !strings.Contains(rec.Body.String(), "toggling account") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})
}

func TestSetAccountDisplayName(t *testing.T) {
	a := newFullTestApp(t)
	ctx := t.Context()
	if err := a.Accounts.Upsert(ctx, models.Account{SimpleFinID: "acc-name", Name: "Name Test", Currency: "USD"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	accts, err := a.Accounts.ListAll(ctx)
	if err != nil || len(accts) != 1 {
		t.Fatalf("ListAll: %+v %v", accts, err)
	}

	t.Run("invalid id", func(t *testing.T) {
		req := postForm(t, "/settings", url.Values{"action": {"set_account_display_name"}, "id": {"nope"}})
		rec := httptest.NewRecorder()
		a.SettingsPage(rec, req)
		if !strings.Contains(rec.Body.String(), "invalid account id") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})

	t.Run("success", func(t *testing.T) {
		req := postForm(t, "/settings", url.Values{"action": {"set_account_display_name"}, "id": {strconv.Itoa(accts[0].ID)}, "display_name": {"My Checking"}})
		rec := httptest.NewRecorder()
		a.SettingsPage(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		updated, err := a.Accounts.ListAll(ctx)
		if err != nil || updated[0].DisplayName.String != "My Checking" {
			t.Fatalf("expected display name set: %+v %v", updated, err)
		}
	})

	t.Run("store failure", func(t *testing.T) {
		broken := *a
		broken.Accounts = &models.AccountStore{DB: brokenBTDB(t)}
		req := postForm(t, "/settings", url.Values{"action": {"set_account_display_name"}, "id": {strconv.Itoa(accts[0].ID)}, "display_name": {"X"}})
		rec := httptest.NewRecorder()
		broken.SettingsPage(rec, req)
		if !strings.Contains(rec.Body.String(), "setting display name") {
			t.Fatalf("body = %s", rec.Body.String())
		}
	})
}

// --- buildSettingsPageData / scheduleDescription ---

func TestBuildSettingsPageDataListAllAccountsFailure(t *testing.T) {
	a := newFullTestApp(t)
	encrypted, err := a.Encryptor.Encrypt("https://x/access")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := a.SimpleFin.Connect(t.Context(), encrypted); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	a.Accounts = &models.AccountStore{DB: brokenBTDB(t)}

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rec := httptest.NewRecorder()
	a.SettingsPage(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestScheduleDescriptionVariants(t *testing.T) {
	tests := []struct {
		name string
		def  models.BillDefinition
		want string
	}{
		{"one_off", models.BillDefinition{ScheduleType: models.ScheduleOneOff, OneOffDate: sql.NullTime{Time: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), Valid: true}}, "One-off: 2026-05-01"},
		{"monthly", models.BillDefinition{ScheduleType: models.ScheduleMonthly, DayOfMonth: sql.NullInt16{Int16: 15, Valid: true}}, "Monthly on day 15"},
		{"quarterly", models.BillDefinition{ScheduleType: models.ScheduleQuarterly, DayOfMonth: sql.NullInt16{Int16: 1, Valid: true}, QuarterStartMonth: sql.NullInt16{Int16: 2, Valid: true}}, "Quarterly (Feb/May/Aug/Nov) on day 1"},
		{"vendor/unknown", models.BillDefinition{ScheduleType: models.ScheduleVendor}, "Unknown schedule"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := scheduleDescription(tc.def); got != tc.want {
				t.Errorf("scheduleDescription() = %q, want %q", got, tc.want)
			}
		})
	}
}
