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
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mscreations/billtracker-plugin/internal/connectors"
)

// hijackTruncate hijacks the connection and writes a response that declares
// more Content-Length than it actually sends, then closes the connection.
// This is the standard trick for forcing a client-side body-read error
// (io.ErrUnexpectedEOF) without needing to modify production code to accept
// an injectable io.Reader - the server-side TCP behavior alone is enough to
// make http.Client's Read (and anything consuming resp.Body, including
// html.Parse when it reads resp.Body directly) fail mid-read.
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
		fmt.Fprintf(bw, "HTTP/1.1 %s\r\n", status)
		bw.WriteString("Content-Length: 99999\r\n\r\n")
		bw.WriteString(body)
		bw.Flush()
		// Closing here (via defer) before the declared Content-Length is
		// satisfied is what triggers the client's unexpected-EOF read error.
	}
}

func TestFetchLoginTokensNetworkError(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1"} // port 0/1 - connection refused
	_, err := c.FetchBill(context.Background(), connectors.Credentials{
		Tenant: "WVWAuthority", Username: "u", Password: "p",
	})
	if err == nil {
		t.Fatal("expected a network error, got nil")
	}
}

func TestFetchLoginTokensNon200Status(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ebpp/WVWAuthority/Login/Index", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{
		Tenant: "WVWAuthority", Username: "u", Password: "p",
	})
	if err == nil {
		t.Fatal("expected an error for a non-200 login page status")
	}
}

func TestFetchLoginTokensBodyReadError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ebpp/WVWAuthority/Login/Index", hijackTruncate(t, "200 OK", "<html><body>not enough bytes"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{
		Tenant: "WVWAuthority", Username: "u", Password: "p",
	})
	if err == nil {
		t.Fatal("expected an error when the login page body read fails mid-stream")
	}
}

func TestFetchLoginTokensMissingToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ebpp/WVWAuthority/Login/Index", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><form></form></body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{
		Tenant: "WVWAuthority", Username: "u", Password: "p",
	})
	if err == nil {
		t.Fatal("expected an error when __RequestVerificationToken is missing")
	}
}

func TestFetchLoginTokensMissingFastPay(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ebpp/WVWAuthority/Login/Index", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><form>
<input type="hidden" name="__RequestVerificationToken" value="tok" />
</form></body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{
		Tenant: "WVWAuthority", Username: "u", Password: "p",
	})
	if err == nil {
		t.Fatal("expected an error when FastPaySelection is missing")
	}
}

// TestFetchBillPostRequestConnectionDropped exercises the httpClient.Do
// error path around the login POST: the Login/Index page is served
// normally, but the POST to /Login hijacks and closes the connection
// without ever writing a response, so httpClient.Do returns an error
// (rather than a response) for the login submission itself.
func TestFetchBillPostRequestConnectionDropped(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ebpp/WVWAuthority/Login/Index", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(loginPageHTML))
	})
	mux.HandleFunc("/ebpp/WVWAuthority/Login", func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("ResponseWriter does not support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		conn.Close() // no response at all
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{
		Tenant: "WVWAuthority", Username: "u", Password: "p",
	})
	if err == nil {
		t.Fatal("expected an error when the login POST connection is dropped")
	}
}

// TestFetchBillPostResponseBodyReadError exercises FetchBill's io.ReadAll
// error path: the login succeeds (302 to BillPay), but the BillPay page
// itself is truncated mid-body.
func TestFetchBillPostResponseBodyReadError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ebpp/WVWAuthority/Login/Index", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(loginPageHTML))
	})
	mux.HandleFunc("/ebpp/WVWAuthority/Login", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ebpp/WVWAuthority/BillPay", http.StatusFound)
	})
	mux.HandleFunc("/ebpp/WVWAuthority/BillPay", hijackTruncate(t, "200 OK", "<html><body>not enough bytes"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{
		Tenant: "WVWAuthority", Username: "u", Password: "p",
	})
	if err == nil {
		t.Fatal("expected an error when the BillPay page body read fails mid-stream")
	}
}

func TestFetchBillBillPayNon200Status(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ebpp/WVWAuthority/Login/Index", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(loginPageHTML))
	})
	mux.HandleFunc("/ebpp/WVWAuthority/Login", func(w http.ResponseWriter, r *http.Request) {
		// 200 (not a redirect) but simulate this landing on /BillPay by
		// having the client-visible resp.Request.URL still be /Login - we
		// instead cover the "status != 200" branch directly by having the
		// (redirected-to) BillPay page itself respond with a non-200.
		http.Redirect(w, r, "/ebpp/WVWAuthority/BillPay", http.StatusFound)
	})
	mux.HandleFunc("/ebpp/WVWAuthority/BillPay", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{
		Tenant: "WVWAuthority", Username: "u", Password: "p",
	})
	if err == nil {
		t.Fatal("expected an error for a non-200 BillPay page status")
	}
}

func TestFetchBillMissingCollectionBodyStillSearchesWholeDocument(t *testing.T) {
	// No "collection-body" wrapper at all - FetchBill should fall back to
	// searching the whole document rather than erroring out immediately,
	// exercising the findFirstByClass "not found" branch.
	const html = `<!DOCTYPE html><html><body>
<span class="cell date due-date">07/20/2026</span>
<span class="cell amount invoice-amount">$45.67</span>
</body></html>`

	mux := http.NewServeMux()
	mux.HandleFunc("/ebpp/WVWAuthority/Login/Index", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(loginPageHTML))
	})
	mux.HandleFunc("/ebpp/WVWAuthority/Login", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ebpp/WVWAuthority/BillPay", http.StatusFound)
	})
	mux.HandleFunc("/ebpp/WVWAuthority/BillPay", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(html))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	snap, err := c.FetchBill(context.Background(), connectors.Credentials{
		Tenant: "WVWAuthority", Username: "u", Password: "p",
	})
	if err != nil {
		t.Fatalf("FetchBill: %v", err)
	}
	if snap.AmountCents != 4567 {
		t.Errorf("AmountCents = %d, want 4567", snap.AmountCents)
	}
}

func TestFetchBillMissingDueDate(t *testing.T) {
	const html = `<!DOCTYPE html><html><body>
<div class="collection-body">
<span class="cell amount invoice-amount">$45.67</span>
</div>
</body></html>`
	c := billPayServer(t, html)
	defer c.srv.Close()

	_, err := c.client.FetchBill(context.Background(), connectors.Credentials{
		Tenant: "WVWAuthority", Username: "u", Password: "p",
	})
	if err == nil {
		t.Fatal("expected an error when due-date is missing")
	}
}

func TestFetchBillMissingInvoiceAmount(t *testing.T) {
	const html = `<!DOCTYPE html><html><body>
<div class="collection-body">
<span class="cell date due-date">07/20/2026</span>
</div>
</body></html>`
	c := billPayServer(t, html)
	defer c.srv.Close()

	_, err := c.client.FetchBill(context.Background(), connectors.Credentials{
		Tenant: "WVWAuthority", Username: "u", Password: "p",
	})
	if err == nil {
		t.Fatal("expected an error when invoice-amount is missing")
	}
}

func TestFetchBillUnparsableDueDate(t *testing.T) {
	const html = `<!DOCTYPE html><html><body>
<div class="collection-body">
<span class="cell date due-date">not-a-date</span>
<span class="cell amount invoice-amount">$45.67</span>
</div>
</body></html>`
	c := billPayServer(t, html)
	defer c.srv.Close()

	_, err := c.client.FetchBill(context.Background(), connectors.Credentials{
		Tenant: "WVWAuthority", Username: "u", Password: "p",
	})
	if err == nil {
		t.Fatal("expected an error for an unparsable due date")
	}
}

func TestFetchBillUnparsableAmount(t *testing.T) {
	const html = `<!DOCTYPE html><html><body>
<div class="collection-body">
<span class="cell date due-date">07/20/2026</span>
<span class="cell amount invoice-amount">TBD</span>
</div>
</body></html>`
	c := billPayServer(t, html)
	defer c.srv.Close()

	_, err := c.client.FetchBill(context.Background(), connectors.Credentials{
		Tenant: "WVWAuthority", Username: "u", Password: "p",
	})
	if err == nil {
		t.Fatal("expected an error for an unparsable invoice amount")
	}
}

type billPayTestServer struct {
	srv    *httptest.Server
	client *Client
}

// billPayServer stands up the login+redirect scaffolding once so the
// missing-field/unparsable-field tests only need to supply the BillPay
// page's body.
func billPayServer(t *testing.T, billPayHTML string) billPayTestServer {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ebpp/WVWAuthority/Login/Index", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(loginPageHTML))
	})
	mux.HandleFunc("/ebpp/WVWAuthority/Login", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ebpp/WVWAuthority/BillPay", http.StatusFound)
	})
	mux.HandleFunc("/ebpp/WVWAuthority/BillPay", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(billPayHTML))
	})
	srv := httptest.NewServer(mux)
	return billPayTestServer{srv: srv, client: &Client{BaseURL: srv.URL}}
}

func TestParseDollarsToCentsErrors(t *testing.T) {
	cases := []string{
		"",                        // no numeric content at all
		"$",                       // strips to empty
		"abc",                     // strips to empty
		".",                       // whole becomes "0", frac becomes "" -> invalid fractional part
		"99999999999999999999.00", // overflows int64 -> invalid whole part
	}
	for _, in := range cases {
		if _, err := parseDollarsToCents(in); err == nil {
			t.Errorf("parseDollarsToCents(%q): expected error, got nil", in)
		}
	}
}

func TestParseDollarsToCentsMoreCases(t *testing.T) {
	cases := map[string]int64{
		".5":    50,  // empty whole part defaults to 0
		"1.234": 123, // fractional part beyond 2 digits is truncated
		"1,234": 123400,
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

func TestParseDueDateAllFormats(t *testing.T) {
	cases := map[string]time.Time{
		"01/02/2026":      time.Date(2026, 1, 2, 0, 0, 0, 0, time.Local),
		"1/2/2026":        time.Date(2026, 1, 2, 0, 0, 0, 0, time.Local),
		"January 2, 2026": time.Date(2026, 1, 2, 0, 0, 0, 0, time.Local),
		"Jan 2, 2026":     time.Date(2026, 1, 2, 0, 0, 0, 0, time.Local),
		"2026-01-02":      time.Date(2026, 1, 2, 0, 0, 0, 0, time.Local),
	}
	for in, want := range cases {
		got, err := parseDueDate(in)
		if err != nil {
			t.Errorf("parseDueDate(%q): %v", in, err)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("parseDueDate(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseDueDateInvalid(t *testing.T) {
	if _, err := parseDueDate("not a date at all"); err == nil {
		t.Fatal("expected an error for an unparsable date")
	}
}

// TestFetchBillUsesInjectedHTTPClient exercises httpClient()'s c.HTTP != nil
// branch - every other test in this package leaves HTTP unset (nil) and so
// only exercises the cookiejar-constructing branch.
func TestFetchBillUsesInjectedHTTPClient(t *testing.T) {
	srv := newTestServer(t, "gooduser", "goodpass")
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: &http.Client{}}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{
		Tenant:   "WVWAuthority",
		Username: "gooduser",
		Password: "goodpass",
	})
	if err != nil {
		t.Fatalf("FetchBill with an injected HTTP client: %v", err)
	}
}

// TestFetchLoginTokensRequestCreationError exercises the
// http.NewRequestWithContext error path in fetchLoginTokens: a Tenant
// containing a control character (here, a newline) makes the constructed
// login-page URL invalid, which fails at request-construction time rather
// than at the network layer.
func TestFetchLoginTokensRequestCreationError(t *testing.T) {
	c := &Client{BaseURL: "http://example.invalid"}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{
		Tenant:   "bad\ntenant",
		Username: "u",
		Password: "p",
	})
	if err == nil {
		t.Fatal("expected an error for a tenant that produces an invalid URL")
	}
}
