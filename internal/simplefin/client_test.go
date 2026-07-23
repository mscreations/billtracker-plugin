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

package simplefin

import (
	"bufio"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// hijackTruncate hijacks the connection and writes a response that declares
// more Content-Length than it actually sends, then closes the connection -
// the standard trick for forcing a client-side body-read error
// (io.ErrUnexpectedEOF) without needing to modify production code to accept
// an injectable io.Reader.
func hijackTruncate(t *testing.T, status string, body string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("ResponseWriter does not support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		defer conn.Close()
		bw := bufio.NewWriter(conn)
		bw.WriteString("HTTP/1.1 " + status + "\r\n")
		bw.WriteString("Content-Length: 99999\r\n\r\n")
		bw.WriteString(body)
		bw.Flush()
	}
}

func TestClaimSetupTokenSuccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/claim/abc", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.Write([]byte("https://user:pass@bridge.example.com/access/xyz"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	token := base64.StdEncoding.EncodeToString([]byte(srv.URL + "/claim/abc"))
	got, err := ClaimSetupToken(context.Background(), token)
	if err != nil {
		t.Fatalf("ClaimSetupToken: %v", err)
	}
	want := "https://user:pass@bridge.example.com/access/xyz"
	if got != want {
		t.Errorf("ClaimSetupToken = %q, want %q", got, want)
	}
}

func TestClaimSetupTokenTrimsSurroundingWhitespaceOnTokenItself(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/claim/abc", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("https://user:pass@bridge.example.com/access/xyz"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	token := "  " + base64.StdEncoding.EncodeToString([]byte(srv.URL+"/claim/abc")) + "\n"
	got, err := ClaimSetupToken(context.Background(), token)
	if err != nil {
		t.Fatalf("ClaimSetupToken: %v", err)
	}
	want := "https://user:pass@bridge.example.com/access/xyz"
	if got != want {
		t.Errorf("ClaimSetupToken = %q, want %q", got, want)
	}
}

func TestClaimSetupTokenInvalidBase64(t *testing.T) {
	_, err := ClaimSetupToken(context.Background(), "not valid base64!!!")
	if err == nil {
		t.Fatal("expected an error for invalid base64")
	}
}

func TestClaimSetupTokenDecodedNotAURL(t *testing.T) {
	token := base64.StdEncoding.EncodeToString([]byte("not a url \x00 at all"))
	_, err := ClaimSetupToken(context.Background(), token)
	if err == nil {
		t.Fatal("expected an error when decoded token is not a URL")
	}
}

func TestClaimSetupTokenRequestCreationError(t *testing.T) {
	// A claim URL containing a raw control character (newline) is valid
	// base64 and passes url.ParseRequestURI's own leniency in some cases,
	// but fails at http.NewRequestWithContext's stricter URL validation.
	token := base64.StdEncoding.EncodeToString([]byte("http://example.invalid/\nbad"))
	_, err := ClaimSetupToken(context.Background(), token)
	if err == nil {
		t.Fatal("expected an error for a claim URL with a control character")
	}
}

func TestClaimSetupTokenNetworkError(t *testing.T) {
	token := base64.StdEncoding.EncodeToString([]byte("http://127.0.0.1:1/claim"))
	_, err := ClaimSetupToken(context.Background(), token)
	if err == nil {
		t.Fatal("expected a network error")
	}
}

func TestClaimSetupTokenBodyReadError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/claim", hijackTruncate(t, "200 OK", "not enough bytes"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	token := base64.StdEncoding.EncodeToString([]byte(srv.URL + "/claim"))
	_, err := ClaimSetupToken(context.Background(), token)
	if err == nil {
		t.Fatal("expected an error when the claim response body read fails mid-stream")
	}
}

func TestClaimSetupTokenNon200Status(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/claim", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("nope"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	token := base64.StdEncoding.EncodeToString([]byte(srv.URL + "/claim"))
	_, err := ClaimSetupToken(context.Background(), token)
	if err == nil {
		t.Fatal("expected an error for a non-200 claim status")
	}
}

func TestClaimSetupTokenResponseNotAURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/claim", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not a url \x00 at all"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	token := base64.StdEncoding.EncodeToString([]byte(srv.URL + "/claim"))
	_, err := ClaimSetupToken(context.Background(), token)
	if err == nil {
		t.Fatal("expected an error when the claim response is not a valid URL")
	}
}

func TestFetchAccountsSuccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("balances-only") != "1" {
			t.Errorf("balances-only param = %q, want 1", r.URL.Query().Get("balances-only"))
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "myuser" || pass != "mypass" {
			t.Errorf("basic auth = %q/%q (ok=%v), want myuser/mypass", user, pass, ok)
		}
		w.Write([]byte(`{
			"accounts": [
				{
					"org": {"name": "Example Bank"},
					"id": "acc-1",
					"name": "Checking",
					"currency": "USD",
					"balance": "1234.56",
					"available-balance": "1200.00",
					"balance-date": 1700000000
				},
				{
					"org": {"name": "Example Bank"},
					"id": "acc-2",
					"name": "Savings",
					"currency": "USD",
					"balance": "0.00"
				}
			]
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	accessURL := "http://myuser:mypass@" + strings.TrimPrefix(srv.URL, "http://") + "/"
	accounts, err := FetchAccounts(context.Background(), accessURL)
	if err != nil {
		t.Fatalf("FetchAccounts: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("len(accounts) = %d, want 2", len(accounts))
	}
	a := accounts[0]
	if a.ID != "acc-1" || a.OrgName != "Example Bank" || a.Name != "Checking" || a.Currency != "USD" {
		t.Errorf("account[0] = %+v", a)
	}
	if a.BalanceCents != 123456 {
		t.Errorf("BalanceCents = %d, want 123456", a.BalanceCents)
	}
	if a.AvailableBalanceCents == nil || *a.AvailableBalanceCents != 120000 {
		t.Errorf("AvailableBalanceCents = %v, want 120000", a.AvailableBalanceCents)
	}
	if a.BalanceDate == nil || !a.BalanceDate.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Errorf("BalanceDate = %v", a.BalanceDate)
	}

	b := accounts[1]
	if b.AvailableBalanceCents != nil {
		t.Errorf("account[1].AvailableBalanceCents = %v, want nil", b.AvailableBalanceCents)
	}
	if b.BalanceDate != nil {
		t.Errorf("account[1].BalanceDate = %v, want nil", b.BalanceDate)
	}
}

func TestFetchAccountsNoBasicAuthWhenNoUserInfo(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok {
			t.Error("expected no basic auth header when access URL has no userinfo")
		}
		w.Write([]byte(`{"accounts": []}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	accounts, err := FetchAccounts(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchAccounts: %v", err)
	}
	if len(accounts) != 0 {
		t.Errorf("len(accounts) = %d, want 0", len(accounts))
	}
}

func TestFetchAccountsInvalidAccessURL(t *testing.T) {
	_, err := FetchAccounts(context.Background(), "http://example.invalid/\nbad")
	if err == nil {
		t.Fatal("expected an error for an invalid access URL")
	}
}

func TestFetchAccountsNetworkError(t *testing.T) {
	_, err := FetchAccounts(context.Background(), "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected a network error")
	}
}

func TestFetchAccountsNon200Status(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("nope"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := FetchAccounts(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected an error for a non-200 accounts status")
	}
}

func TestFetchAccountsInvalidJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := FetchAccounts(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

func TestFetchAccountsReportedErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"accounts": [], "errors": ["bad token", "expired"]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := FetchAccounts(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected an error when SimpleFIN reports errors")
	}
	if !strings.Contains(err.Error(), "bad token") || !strings.Contains(err.Error(), "expired") {
		t.Errorf("error should mention both reported errors, got: %v", err)
	}
}

func TestFetchAccountsUnparsableBalance(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"accounts": [{"id": "acc-1", "balance": "not-a-number"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := FetchAccounts(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected an error for an unparsable balance")
	}
}

func TestFetchAccountsUnparsableAvailableBalanceIsIgnored(t *testing.T) {
	// An unparsable available-balance is silently dropped (left nil) rather
	// than failing the whole account, since the primary balance parsed fine.
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"accounts": [{"id": "acc-1", "balance": "10.00", "available-balance": "not-a-number"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	accounts, err := FetchAccounts(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("FetchAccounts: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("len(accounts) = %d, want 1", len(accounts))
	}
	if accounts[0].AvailableBalanceCents != nil {
		t.Errorf("AvailableBalanceCents = %v, want nil", accounts[0].AvailableBalanceCents)
	}
}

func TestParseMoneyToCentsCases(t *testing.T) {
	cases := map[string]int64{
		"1234.5": 123450,
		"-42":    -4200,
		"0.07":   7,
		"+5.50":  550,
		".5":     50,
		"1.234":  123,
		"100":    10000,
		"5.1":    510,
	}
	for in, want := range cases {
		got, err := parseMoneyToCents(in)
		if err != nil {
			t.Errorf("parseMoneyToCents(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseMoneyToCents(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseMoneyToCentsErrors(t *testing.T) {
	cases := []string{
		"",
		"   ",
		".",
		"99999999999999999999.00",
	}
	for _, in := range cases {
		if _, err := parseMoneyToCents(in); err == nil {
			t.Errorf("parseMoneyToCents(%q): expected error, got nil", in)
		}
	}
}
