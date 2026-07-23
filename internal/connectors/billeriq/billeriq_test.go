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

package billeriq

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mscreations/billtracker-plugin/internal/connectors"
)

const loginPageHTML = `<!DOCTYPE html><html><body>
<form method="post" action="/ebpp/WVWAuthority/Login">
<input type="hidden" name="__RequestVerificationToken" value="tok-abc123" />
<input type="hidden" name="FastPaySelection" value="" />
<input type="text" name="Login" />
<input type="password" name="Password" />
</form>
</body></html>`

// billPayHTML mirrors the real BillerIQ markup shape: the sortable column
// headers inside collection-header reuse the very same class names
// ("due-date", "invoice-amount") as the actual invoice value spans inside
// collection-body, just with the column title as their text instead of a
// value. findTextByClass must be scoped to collection-body or it returns
// "Due Date"/"Amount Billed" instead of the real values - see billeriq.go's
// searchRoot handling.
const billPayHTML = `<!DOCTYPE html><html><body>
<div class="collection-header invoices-header">
  <span class="cell due-date sortable">Due Date</span>
  <span class="cell amount invoice-amount sortable">Amount Billed</span>
</div>
<div class="collection-body invoices-body">
  <div class="account-display-first-line">Account: 000123456</div>
  <span class="cell date due-date">07/20/2026</span>
  <span class="cell amount invoice-amount">$45.67</span>
</div>
</body></html>`

// newTestServer mimics the real billeriq flow closely enough to exercise
// Client.FetchBill end-to-end: it inspects the login POST for the scraped
// hidden-input values and the submitted credentials, only "succeeding"
// (302 to BillPay) when they match, and 200s the login form back
// otherwise - the same failure signal real billeriq gives on a bad
// password (landing back on Login instead of BillPay).
func newTestServer(t *testing.T, wantUsername, wantPassword string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ebpp/WVWAuthority/Login/Index", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(loginPageHTML))
	})
	mux.HandleFunc("/ebpp/WVWAuthority/Login", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parsing login form: %v", err)
		}
		if r.FormValue("__RequestVerificationToken") != "tok-abc123" {
			t.Errorf("login POST missing/incorrect __RequestVerificationToken: %q", r.FormValue("__RequestVerificationToken"))
		}
		if r.FormValue("Login") == wantUsername && r.FormValue("Password") == wantPassword {
			http.Redirect(w, r, "/ebpp/WVWAuthority/BillPay", http.StatusFound)
			return
		}
		w.Write([]byte(loginPageHTML))
	})
	mux.HandleFunc("/ebpp/WVWAuthority/BillPay", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(billPayHTML))
	})
	return httptest.NewServer(mux)
}

func TestFetchBillSuccess(t *testing.T) {
	srv := newTestServer(t, "gooduser", "goodpass")
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	snap, err := c.FetchBill(context.Background(), connectors.Credentials{
		Tenant:   "WVWAuthority",
		Username: "gooduser",
		Password: "goodpass",
	})
	if err != nil {
		t.Fatalf("FetchBill: %v", err)
	}
	if snap.AmountCents != 4567 {
		t.Errorf("AmountCents = %d, want 4567", snap.AmountCents)
	}
	wantDue := time.Date(2026, 7, 20, 0, 0, 0, 0, time.Local)
	if !snap.DueDate.Equal(wantDue) {
		t.Errorf("DueDate = %v, want %v", snap.DueDate, wantDue)
	}
	if snap.AccountNumber != "Account: 000123456" {
		t.Errorf("AccountNumber = %q", snap.AccountNumber)
	}
}

func TestFetchBillWrongPasswordReturnsError(t *testing.T) {
	srv := newTestServer(t, "gooduser", "goodpass")
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{
		Tenant:   "WVWAuthority",
		Username: "gooduser",
		Password: "wrongpass",
	})
	if err == nil {
		t.Fatal("expected an error for a wrong password, got nil")
	}
}

func TestFetchBillRequiresTenant(t *testing.T) {
	c := &Client{BaseURL: "http://example.invalid"}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{Username: "u", Password: "p"})
	if err == nil {
		t.Fatal("expected an error for a missing tenant, got nil")
	}
}

func TestParseDollarsToCents(t *testing.T) {
	cases := map[string]int64{
		"$45.67":    4567,
		"$1,234.50": 123450,
		"45.6":      4560,
		"45":        4500,
		"-$5.00":    -500,
	}
	for in, want := range cases {
		got, err := parseDollarsToCents(in)
		if err != nil {
			t.Errorf("parseDollarsToCents(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseDollarsToCents(%q) = %d, want %d", in, got, want)
		}
	}
}
