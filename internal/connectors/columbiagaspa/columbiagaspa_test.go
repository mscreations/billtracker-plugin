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

package columbiagaspa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mscreations/billtracker-plugin/internal/connectors"
)

// dashboardHTML mirrors the real account-summary markup shape reported
// from a live login (see the connector's package doc for provenance).
const dashboardHTML = `<!DOCTYPE html><html><body>
<div class="box box--balance box--balance-details">
    <h2>
        Account Summary
        <a href="/bills/latest" target="_blank">View Current Bill</a>
    </h2>
    <div class="box__content box__content--flex row">
    </div>
    <div id="page-alerts"></div>
    <div class="box__content box__content--flex row box__balance-due">
        <div class="balance-due col col-xs-12 col-sm-12 col-md-7">
            <p>
                <strong class="h3 ">Current Charges Due</strong><br>
                <span class="h3 hide-md hide-lg ">$123.45<br /></span>
                <span class="balance-due-on ">Due on Jul 20, 2026
</span>
            </p>
        </div>
        <div class="balance-due col col-xs-12 col-sm-6 col-md-5 hide-xs hide-sm">
            <span class="h1 ">$123.45</span>
        </div>
    </div>
</div>
</body></html>`

// loginFormHTML is what a rejected login re-renders - no
// box--balance-details section present.
const loginFormHTML = `<!DOCTYPE html><html><body>
<form method="post" action="/login">
<input type="text" name="username" />
<input type="password" name="password" />
</form>
</body></html>`

func newTestServer(t *testing.T, wantUsername, wantPassword string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parsing login form: %v", err)
		}
		if r.FormValue("rememberme") != "true" {
			t.Errorf("login POST missing rememberme=true: %q", r.FormValue("rememberme"))
		}
		if r.FormValue("username") == wantUsername && r.FormValue("password") == wantPassword {
			w.Write([]byte(dashboardHTML))
			return
		}
		w.Write([]byte(loginFormHTML))
	})
	return httptest.NewServer(mux)
}

func TestFetchBillSuccess(t *testing.T) {
	srv := newTestServer(t, "gooduser", "goodpass")
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	snap, err := c.FetchBill(context.Background(), connectors.Credentials{
		Username: "gooduser",
		Password: "goodpass",
	})
	if err != nil {
		t.Fatalf("FetchBill: %v", err)
	}
	if snap.AmountCents != 12345 {
		t.Errorf("AmountCents = %d, want 12345", snap.AmountCents)
	}
	wantDue := time.Date(2026, 7, 20, 0, 0, 0, 0, time.Local)
	if !snap.DueDate.Equal(wantDue) {
		t.Errorf("DueDate = %v, want %v", snap.DueDate, wantDue)
	}
}

func TestFetchBillWrongPasswordReturnsError(t *testing.T) {
	srv := newTestServer(t, "gooduser", "goodpass")
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{
		Username: "gooduser",
		Password: "wrongpass",
	})
	if err == nil {
		t.Fatal("expected an error for a wrong password, got nil")
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

func TestParseDueDate(t *testing.T) {
	got, err := parseDueDate("Due on Jul 20, 2026\n")
	if err != nil {
		t.Fatalf("parseDueDate: %v", err)
	}
	want := time.Date(2026, 7, 20, 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("parseDueDate = %v, want %v", got, want)
	}
}
