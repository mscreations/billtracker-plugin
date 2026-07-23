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

// Package columbiagaspa implements internal/connectors.Connector for
// myaccount.columbiagaspa.com, Columbia Gas of Pennsylvania's account
// portal. Unlike billeriq, this site has no per-tenant path segment and no
// scraped anti-forgery token was needed to reproduce a working login (see
// the connector's tests for the exact HTML shapes expected) - Tenant on
// Credentials is unused here.
//
// Flow (reverse-engineered from a real login):
//  1. POST /login with username, password, and rememberme=true
//     (form-urlencoded). A successful login's response body IS the account
//     dashboard HTML directly (no redirect to follow) - a failed login
//     re-renders the login form instead, which lacks the dashboard's
//     "box--balance-details" section.
//  2. Scrape the current-charges amount from span.h1 and the due date from
//     span.balance-due-on, both scoped inside div.box--balance-details to
//     avoid accidentally matching an unrelated element elsewhere on the
//     page.
package columbiagaspa

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/mscreations/billtracker-plugin/internal/connectors"
)

const slug = "columbiagaspa"

// DefaultBaseURL is exported as a var (not a const) so tests can point it
// at a local httptest server.
var DefaultBaseURL = "https://myaccount.columbiagaspa.com"

const requestTimeout = 20 * time.Second

// userAgent mirrors a real browser's UA - the site appeared to require one
// (or at least the working reverse-engineered login always sent one).
const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New() *Client {
	return &Client{BaseURL: DefaultBaseURL}
}

func (c *Client) Slug() string { return slug }

func init() {
	connectors.Register(New())
}

// httpClient returns c.HTTP if set (used by tests to inject a client
// pointed at a local httptest server), otherwise a fresh client with a
// cookie jar. cookiejar.New(nil) never actually returns an error (confirmed
// against the stdlib implementation - a nil *Options is always valid), so
// unlike the rest of this file's error handling, there is no failure mode
// to propagate here.
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

// FetchBill logs in as creds.Username/Password and scrapes the resulting
// account dashboard page. creds.Tenant is unused for this connector.
func (c *Client) FetchBill(ctx context.Context, creds connectors.Credentials) (connectors.BillSnapshot, error) {
	httpClient := c.httpClient()

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	base := strings.TrimRight(c.BaseURL, "/")
	loginPostURL := base + "/login"

	form := url.Values{}
	form.Set("username", creds.Username)
	form.Set("password", creds.Password)
	form.Set("rememberme", "true")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginPostURL, strings.NewReader(form.Encode()))
	if err != nil {
		return connectors.BillSnapshot{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Origin", "https://www.columbiagaspa.com")
	req.Header.Set("Referer", "https://www.columbiagaspa.com/")

	resp, err := httpClient.Do(req)
	if err != nil {
		return connectors.BillSnapshot{}, fmt.Errorf("columbiagaspa: logging in: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return connectors.BillSnapshot{}, fmt.Errorf("columbiagaspa: reading login response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return connectors.BillSnapshot{}, fmt.Errorf("columbiagaspa: unexpected status %d logging in", resp.StatusCode)
	}

	// html.Parse only ever returns a non-nil error when its Reader's Read
	// method fails; strings.Reader (built from the already-fully-read body
	// above) never does, so there is no failure mode to check here.
	doc, _ := html.Parse(strings.NewReader(string(body)))

	// A failed login re-renders the login form, which has no
	// box--balance-details section - that section's absence is the
	// clearest signal we have that the credentials were rejected, since
	// the site gives no distinct error element to key off of.
	searchRoot, ok := findFirstByClass(doc, "box--balance-details")
	if !ok {
		return connectors.BillSnapshot{}, fmt.Errorf("columbiagaspa: login did not reach the account dashboard - check username/password")
	}

	dueDateText, ok := findTextByClass(searchRoot, "balance-due-on")
	if !ok {
		return connectors.BillSnapshot{}, fmt.Errorf("columbiagaspa: balance-due-on not found on account page")
	}
	amountText, ok := findTextByClass(searchRoot, "h1")
	if !ok {
		return connectors.BillSnapshot{}, fmt.Errorf("columbiagaspa: current-charges amount not found on account page")
	}

	dueDate, err := parseDueDate(dueDateText)
	if err != nil {
		return connectors.BillSnapshot{}, fmt.Errorf("columbiagaspa: parsing due date %q: %w", dueDateText, err)
	}
	amountCents, err := parseDollarsToCents(amountText)
	if err != nil {
		return connectors.BillSnapshot{}, fmt.Errorf("columbiagaspa: parsing current charges %q: %w", amountText, err)
	}

	return connectors.BillSnapshot{
		AmountCents: amountCents,
		DueDate:     dueDate,
	}, nil
}

// findFirstByClass walks the tree for the first element carrying class in
// its space-separated class attribute and returns that element itself
// (unlike findTextByClass, which returns its text content).
func findFirstByClass(n *html.Node, class string) (*html.Node, bool) {
	if n.Type == html.ElementNode && hasClass(n, class) {
		return n, true
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if v, ok := findFirstByClass(child, class); ok {
			return v, true
		}
	}
	return nil, false
}

// findTextByClass walks the tree for the first element carrying class in
// its space-separated class attribute and returns its trimmed text
// content.
func findTextByClass(n *html.Node, class string) (string, bool) {
	if n.Type == html.ElementNode && hasClass(n, class) {
		return strings.TrimSpace(textContent(n)), true
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if v, ok := findTextByClass(child, class); ok {
			return v, true
		}
	}
	return "", false
}

func hasClass(n *html.Node, class string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

func textContent(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return sb.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

var dollarStripRe = regexp.MustCompile(`[^0-9.\-]`)

// parseDollarsToCents converts vendor-formatted currency text (e.g.
// "$45.67", "$1,234.50") to integer cents. Parsed via strconv on the
// stripped-down decimal string, not a float, to avoid floating-point
// rounding error on money values.
func parseDollarsToCents(s string) (int64, error) {
	stripped := dollarStripRe.ReplaceAllString(s, "")
	if stripped == "" {
		return 0, fmt.Errorf("no numeric content")
	}
	neg := strings.HasPrefix(stripped, "-")
	stripped = strings.TrimPrefix(stripped, "-")

	whole, frac, hasFrac := strings.Cut(stripped, ".")
	if whole == "" {
		whole = "0"
	}
	switch {
	case !hasFrac:
		frac = "00"
	case len(frac) == 1:
		frac += "0"
	case len(frac) > 2:
		frac = frac[:2]
	}

	wholeCents, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid whole part: %w", err)
	}
	fracCents, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid fractional part: %w", err)
	}

	total := wholeCents*100 + fracCents
	if neg {
		total = -total
	}
	return total, nil
}

// dueDateOnPrefixRe strips the "Due on " prefix (and any trailing
// whitespace/newline the site's template leaves in the span) from the
// scraped balance-due-on text, leaving just the date, e.g. "Jul 20, 2026".
var dueDateOnPrefixRe = regexp.MustCompile(`(?i)^Due on\s+`)

func parseDueDate(s string) (time.Time, error) {
	s = dueDateOnPrefixRe.ReplaceAllString(strings.TrimSpace(s), "")
	s = strings.TrimSpace(s)
	if t, err := time.ParseInLocation("Jan 2, 2006", s, time.Local); err == nil {
		return t, nil
	}
	return time.ParseInLocation("January 2, 2006", s, time.Local)
}
