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

// Package simplefin is a minimal client for the SimpleFIN Bridge protocol
// (https://www.simplefin.org/protocol.html) - just enough to claim a
// one-time setup token and fetch account balances. No transaction history,
// no write operations.
package simplefin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	claimTimeout    = 15 * time.Second
	accountsTimeout = 20 * time.Second
)

// InsecureTestMode disables both the https-only requirement and the
// private/loopback/link-local/multicast dial block below. Exported (despite
// the risk of misuse) solely so other packages' tests exercising code paths
// that call into this package (e.g. internal/scheduler's SimpleFIN tests)
// can point at a local httptest server, same as this package's own tests
// (see client_test.go's withInsecureTestMode) - never set outside a test.
var InsecureTestMode = false

// httpClient dials through safeDialContext instead of using
// http.DefaultClient - both the claim URL (decoded from a "setup token" a
// parent pastes into the settings page) and the access URL (persisted from
// a prior claim) are fully attacker-controlled from this package's point of
// view, so every outbound request in this file needs to go through this
// client to guard against SSRF into internal network targets.
var httpClient = &http.Client{
	Transport: &http.Transport{
		DialContext: safeDialContext,
	},
}

// safeDialContext resolves the target host and refuses to connect if the
// address it actually dials is loopback/private/link-local/multicast -
// resolving and checking the IP at dial time (rather than validating a URL
// up front) closes the DNS-rebinding gap where a host could resolve to a
// public IP at validation time and a private one moments later.
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolving %q: %w", host, err)
	}
	var chosen net.IP
	for _, ip := range ips {
		if InsecureTestMode || !isDisallowedIP(ip) {
			chosen = ip
			break
		}
	}
	if chosen == nil {
		return nil, fmt.Errorf("host %q has no allowed address (refusing loopback/private/link-local/multicast targets)", host)
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return dialer.DialContext(ctx, network, net.JoinHostPort(chosen.String(), port))
}

func isDisallowedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}

// ClaimSetupToken exchanges a one-time SimpleFIN setup token (as pasted by
// the parent into the settings page) for a persistent access URL. The setup
// token is a base64-encoded claim URL; POSTing to that claim URL (empty
// body) returns the access URL in the response body. The access URL embeds
// HTTP Basic Auth credentials and must be stored encrypted at rest (see
// internal/util.Encryptor) - this function only performs the exchange.
func ClaimSetupToken(ctx context.Context, setupToken string) (string, error) {
	claimURLBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(setupToken))
	if err != nil {
		return "", fmt.Errorf("setup token is not valid base64: %w", err)
	}
	claimURL := string(claimURLBytes)
	parsedClaimURL, err := url.ParseRequestURI(claimURL)
	if err != nil {
		return "", fmt.Errorf("decoded setup token is not a valid URL: %w", err)
	}
	// Credentials travel back from the bridge over this same connection
	// (see ClaimSetupToken's doc comment - the access URL embeds HTTP Basic
	// Auth), so a plaintext http:// target would leak them; require https.
	if !InsecureTestMode && parsedClaimURL.Scheme != "https" {
		return "", fmt.Errorf("decoded setup token URL must use https")
	}

	ctx, cancel := context.WithTimeout(ctx, claimTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claimURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("claiming setup token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading claim response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("claiming setup token: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	accessURL := strings.TrimSpace(string(body))
	if _, err := url.ParseRequestURI(accessURL); err != nil {
		return "", fmt.Errorf("claim response is not a valid access URL: %w", err)
	}
	return accessURL, nil
}

// Account is one bank/card account as returned by SimpleFIN's /accounts
// endpoint (only the fields this plugin uses).
type Account struct {
	ID                    string
	OrgName               string
	Name                  string
	Currency              string
	BalanceCents          int64
	AvailableBalanceCents *int64
	BalanceDate           *time.Time
}

type simplefinAccountsResponse struct {
	Accounts []struct {
		Org struct {
			Name string `json:"name"`
		} `json:"org"`
		ID               string `json:"id"`
		Name             string `json:"name"`
		Currency         string `json:"currency"`
		Balance          string `json:"balance"`
		AvailableBalance string `json:"available-balance"`
		BalanceDate      int64  `json:"balance-date"` // unix seconds
	} `json:"accounts"`
	Errors []string `json:"errors"`
}

// FetchAccounts fetches current balances for every account visible to
// accessURL's embedded credentials. balances-only=1 skips transaction
// history, which this plugin never needs.
func FetchAccounts(ctx context.Context, accessURL string) ([]Account, error) {
	ctx, cancel := context.WithTimeout(ctx, accountsTimeout)
	defer cancel()

	u, err := url.Parse(strings.TrimRight(accessURL, "/") + "/accounts")
	if err != nil {
		return nil, fmt.Errorf("invalid access URL: %w", err)
	}
	if !InsecureTestMode && u.Scheme != "https" {
		return nil, fmt.Errorf("access URL must use https")
	}
	q := u.Query()
	q.Set("balances-only", "1")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if u.User != nil {
		password, _ := u.User.Password()
		req.SetBasicAuth(u.User.Username(), password)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching accounts: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("fetching accounts: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var decoded simplefinAccountsResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decoding accounts response: %w", err)
	}
	if len(decoded.Errors) > 0 {
		return nil, fmt.Errorf("SimpleFIN reported errors: %s", strings.Join(decoded.Errors, "; "))
	}

	accounts := make([]Account, 0, len(decoded.Accounts))
	for _, a := range decoded.Accounts {
		balanceCents, err := parseMoneyToCents(a.Balance)
		if err != nil {
			return nil, fmt.Errorf("account %s: parsing balance %q: %w", a.ID, a.Balance, err)
		}
		out := Account{
			ID:           a.ID,
			OrgName:      a.Org.Name,
			Name:         a.Name,
			Currency:     a.Currency,
			BalanceCents: balanceCents,
		}
		if a.AvailableBalance != "" {
			if avail, err := parseMoneyToCents(a.AvailableBalance); err == nil {
				out.AvailableBalanceCents = &avail
			}
		}
		if a.BalanceDate > 0 {
			t := time.Unix(a.BalanceDate, 0).UTC()
			out.BalanceDate = &t
		}
		accounts = append(accounts, out)
	}
	return accounts, nil
}

// parseMoneyToCents converts a SimpleFIN decimal-string amount (e.g.
// "1234.5", "-42", "0.07") into integer cents. Parsed as a string, not a
// float, to avoid floating-point rounding error on money values.
func parseMoneyToCents(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty amount")
	}
	neg := false
	switch {
	case strings.HasPrefix(s, "-"):
		neg = true
		s = s[1:]
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	}

	whole, frac, hasFrac := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	switch {
	case !hasFrac:
		frac = "00"
	case len(frac) == 1:
		frac += "0"
	case len(frac) > 2:
		frac = frac[:2] // truncate sub-cent precision
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
