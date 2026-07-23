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
		fmt.Fprintf(bw, "HTTP/1.1 %s\r\n", status)
		bw.WriteString("Content-Length: 99999\r\n\r\n")
		bw.WriteString(body)
		bw.Flush()
	}
}

func TestFetchBillUsesInjectedHTTPClient(t *testing.T) {
	srv := newTestServer(t, "gooduser", "goodpass")
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: &http.Client{}}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{
		Username: "gooduser",
		Password: "goodpass",
	})
	if err != nil {
		t.Fatalf("FetchBill with an injected HTTP client: %v", err)
	}
}

// TestFetchBillRequestCreationError exercises the http.NewRequestWithContext
// error path: a BaseURL containing a control character (a newline) makes
// the constructed login URL invalid, failing at request-construction time.
func TestFetchBillRequestCreationError(t *testing.T) {
	c := &Client{BaseURL: "http://example.invalid/\nbad"}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{Username: "u", Password: "p"})
	if err == nil {
		t.Fatal("expected an error for a BaseURL that produces an invalid URL")
	}
}

func TestFetchBillNetworkError(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1"}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{Username: "u", Password: "p"})
	if err == nil {
		t.Fatal("expected a network error, got nil")
	}
}

func TestFetchBillLoginResponseBodyReadError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", hijackTruncate(t, "200 OK", "<html><body>not enough bytes"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{Username: "u", Password: "p"})
	if err == nil {
		t.Fatal("expected an error when the login response body read fails mid-stream")
	}
}

func TestFetchBillNon200Status(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{BaseURL: srv.URL}
	_, err := c.FetchBill(context.Background(), connectors.Credentials{Username: "u", Password: "p"})
	if err == nil {
		t.Fatal("expected an error for a non-200 login status")
	}
}

func TestFetchBillMissingBalanceDueOn(t *testing.T) {
	const html = `<!DOCTYPE html><html><body>
<div class="box--balance-details">
<span class="h1">$123.45</span>
</div>
</body></html>`
	c := dashboardServer(t, html)
	defer c.srv.Close()

	_, err := c.client.FetchBill(context.Background(), connectors.Credentials{Username: "u", Password: "p"})
	if err == nil {
		t.Fatal("expected an error when balance-due-on is missing")
	}
}

func TestFetchBillMissingH1Amount(t *testing.T) {
	const html = `<!DOCTYPE html><html><body>
<div class="box--balance-details">
<span class="balance-due-on">Due on Jul 20, 2026</span>
</div>
</body></html>`
	c := dashboardServer(t, html)
	defer c.srv.Close()

	_, err := c.client.FetchBill(context.Background(), connectors.Credentials{Username: "u", Password: "p"})
	if err == nil {
		t.Fatal("expected an error when the h1 amount is missing")
	}
}

func TestFetchBillUnparsableDueDate(t *testing.T) {
	const html = `<!DOCTYPE html><html><body>
<div class="box--balance-details">
<span class="balance-due-on">not-a-date</span>
<span class="h1">$123.45</span>
</div>
</body></html>`
	c := dashboardServer(t, html)
	defer c.srv.Close()

	_, err := c.client.FetchBill(context.Background(), connectors.Credentials{Username: "u", Password: "p"})
	if err == nil {
		t.Fatal("expected an error for an unparsable due date")
	}
}

func TestFetchBillUnparsableAmount(t *testing.T) {
	const html = `<!DOCTYPE html><html><body>
<div class="box--balance-details">
<span class="balance-due-on">Due on Jul 20, 2026</span>
<span class="h1">TBD</span>
</div>
</body></html>`
	c := dashboardServer(t, html)
	defer c.srv.Close()

	_, err := c.client.FetchBill(context.Background(), connectors.Credentials{Username: "u", Password: "p"})
	if err == nil {
		t.Fatal("expected an error for an unparsable amount")
	}
}

type dashboardTestServer struct {
	srv    *httptest.Server
	client *Client
}

func dashboardServer(t *testing.T, html string) dashboardTestServer {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(html))
	})
	srv := httptest.NewServer(mux)
	return dashboardTestServer{srv: srv, client: &Client{BaseURL: srv.URL}}
}

func TestParseDollarsToCentsErrors(t *testing.T) {
	cases := []string{
		"",
		"$",
		"abc",
		".",
		"99999999999999999999.00",
	}
	for _, in := range cases {
		if _, err := parseDollarsToCents(in); err == nil {
			t.Errorf("parseDollarsToCents(%q): expected error, got nil", in)
		}
	}
}

func TestParseDollarsToCentsMoreCases(t *testing.T) {
	cases := map[string]int64{
		".5":    50,
		"1.234": 123,
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

func TestParseDueDateFullMonthName(t *testing.T) {
	// "Jan 2, 2006" fails to match a full month name, so this exercises the
	// fallback "January 2, 2006" format.
	got, err := parseDueDate("Due on July 20, 2026")
	if err != nil {
		t.Fatalf("parseDueDate: %v", err)
	}
	want := time.Date(2026, 7, 20, 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("parseDueDate = %v, want %v", got, want)
	}
}

func TestParseDueDateInvalid(t *testing.T) {
	if _, err := parseDueDate("not a date at all"); err == nil {
		t.Fatal("expected an error for an unparsable date")
	}
}
