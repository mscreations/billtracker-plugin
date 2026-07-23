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

// Package billeriq implements internal/connectors.Connector for
// billeriq.com, a white-labeled ASP.NET bill-pay platform used by many
// (mostly utility) vendors - each vendor gets its own path segment under a
// shared host, e.g. https://fnbezinvoice.billeriq.com/ebpp/WVWAuthority/...
// The Credentials.Tenant field is that path segment ("WVWAuthority").
//
// Flow (reverse-engineered from a real login, see the connector's tests for
// the exact HTML shapes expected):
//  1. GET  /ebpp/{tenant}/Login/Index - scrape the anti-forgery token
//     (__RequestVerificationToken) and FastPaySelection hidden inputs.
//  2. POST /ebpp/{tenant}/Login with Login, Password, and both hidden
//     values. On success this 302s to /ebpp/{tenant}/BillPay; Go's
//     http.Client follows POST->GET redirects for 302 automatically, so a
//     successful login lands directly on the BillPay page's HTML.
//  3. Scrape span.due-date, span.invoice-amount, and
//     div.account-display-first-line from that page.
package billeriq

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

const slug = "billeriq"

// DefaultBaseURL is billeriq's shared host - every tenant lives under
// /ebpp/{tenant} on this same host. Exported as a var (not a const) so
// tests can point it at a local httptest server.
var DefaultBaseURL = "https://fnbezinvoice.billeriq.com"

const requestTimeout = 20 * time.Second

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
// cookie jar so the login flow's session cookie is retained across the
// GET-login-page/POST-login/follow-redirect sequence. cookiejar.New(nil)
// never actually returns an error (confirmed against the stdlib
// implementation - a nil *Options is always valid), so unlike the rest of
// this file's error handling, there is no failure mode to propagate here.
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

// FetchBill logs in as creds.Username/Password against
// /ebpp/{creds.Tenant} and scrapes the resulting BillPay page.
func (c *Client) FetchBill(ctx context.Context, creds connectors.Credentials) (connectors.BillSnapshot, error) {
	if creds.Tenant == "" {
		return connectors.BillSnapshot{}, fmt.Errorf("billeriq: tenant is required")
	}

	httpClient := c.httpClient()

	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	base := strings.TrimRight(c.BaseURL, "/")
	loginPageURL := fmt.Sprintf("%s/ebpp/%s/Login/Index", base, creds.Tenant)
	loginPostURL := fmt.Sprintf("%s/ebpp/%s/Login", base, creds.Tenant)

	token, fastPay, err := fetchLoginTokens(ctx, httpClient, loginPageURL)
	if err != nil {
		return connectors.BillSnapshot{}, fmt.Errorf("billeriq: fetching login page: %w", err)
	}

	form := url.Values{}
	form.Set("Login", creds.Username)
	form.Set("Password", creds.Password)
	form.Set("__RequestVerificationToken", token)
	form.Set("FastPaySelection", fastPay)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginPostURL, strings.NewReader(form.Encode()))
	if err != nil {
		return connectors.BillSnapshot{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient.Do(req)
	if err != nil {
		return connectors.BillSnapshot{}, fmt.Errorf("billeriq: logging in: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return connectors.BillSnapshot{}, fmt.Errorf("billeriq: reading BillPay response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return connectors.BillSnapshot{}, fmt.Errorf("billeriq: unexpected status %d fetching BillPay page", resp.StatusCode)
	}
	// A failed login re-renders the login form rather than reaching
	// BillPay - the redirected URL's path is the clearest signal of that,
	// since the login page and BillPay page share no distinguishing DOM
	// markers we've already committed to scraping.
	if !strings.Contains(resp.Request.URL.Path, "/BillPay") {
		return connectors.BillSnapshot{}, fmt.Errorf("billeriq: login did not reach BillPay (landed on %s) - check username/password", resp.Request.URL.Path)
	}

	// html.Parse only ever returns a non-nil error when its Reader's Read
	// method fails; strings.Reader (built from the already-fully-read body
	// above) never does, so there is no failure mode to check here.
	doc, _ := html.Parse(strings.NewReader(string(body)))

	// The BillPay page reuses the same class names (e.g. "due-date",
	// "invoice-amount") on both the sortable column-header span inside
	// "collection-header" and the actual value span for each invoice
	// inside "collection-body" - a plain document-order search finds the
	// header label first. Scope the search to collection-body so we land
	// on the real values instead of the column titles.
	searchRoot := doc
	if body, ok := findFirstByClass(doc, "collection-body"); ok {
		searchRoot = body
	}

	dueDateText, ok := findTextByClass(searchRoot, "due-date")
	if !ok {
		return connectors.BillSnapshot{}, fmt.Errorf("billeriq: due-date not found on BillPay page")
	}
	amountText, ok := findTextByClass(searchRoot, "invoice-amount")
	if !ok {
		return connectors.BillSnapshot{}, fmt.Errorf("billeriq: invoice-amount not found on BillPay page")
	}
	acctText, _ := findTextByClass(searchRoot, "account-display-first-line")

	dueDate, err := parseDueDate(dueDateText)
	if err != nil {
		return connectors.BillSnapshot{}, fmt.Errorf("billeriq: parsing due date %q: %w", dueDateText, err)
	}
	amountCents, err := parseDollarsToCents(amountText)
	if err != nil {
		return connectors.BillSnapshot{}, fmt.Errorf("billeriq: parsing invoice amount %q: %w", amountText, err)
	}

	return connectors.BillSnapshot{
		AmountCents:   amountCents,
		DueDate:       dueDate,
		AccountNumber: strings.TrimSpace(acctText),
	}, nil
}

// fetchLoginTokens GETs the login page and scrapes the two hidden inputs
// the login POST requires.
func fetchLoginTokens(ctx context.Context, httpClient *http.Client, loginPageURL string) (token, fastPay string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loginPageURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("parsing login page: %w", err)
	}

	token, ok := findHiddenInputValue(doc, "__RequestVerificationToken")
	if !ok {
		return "", "", fmt.Errorf("__RequestVerificationToken not found on login page")
	}
	// FastPaySelection may legitimately be present-but-empty; only its
	// absence from the DOM entirely is an error.
	fastPay, ok = findHiddenInputValue(doc, "FastPaySelection")
	if !ok {
		return "", "", fmt.Errorf("FastPaySelection not found on login page")
	}
	return token, fastPay, nil
}

// findHiddenInputValue walks the tree for <input name="name" value="...">
// and returns its value attribute.
func findHiddenInputValue(n *html.Node, name string) (string, bool) {
	if n.Type == html.ElementNode && n.Data == "input" {
		if attr(n, "name") == name {
			return attr(n, "value"), true
		}
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if v, ok := findHiddenInputValue(child, name); ok {
			return v, true
		}
	}
	return "", false
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

var dueDateFormats = []string{
	"01/02/2006",
	"1/2/2006",
	"January 2, 2006",
	"Jan 2, 2006",
	"2006-01-02",
}

func parseDueDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	var lastErr error
	for _, format := range dueDateFormats {
		if t, err := time.ParseInLocation(format, s, time.Local); err == nil {
			return t, nil
		} else {
			lastErr = err
		}
	}
	return time.Time{}, lastErr
}
