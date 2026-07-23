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
	"testing"

	"github.com/mscreations/billtracker-plugin/internal/models"
)

func TestCurrentTokenRejectsNonHexStoredValue(t *testing.T) {
	a := newTestApp(t)
	if err := a.Settings.Set(t.Context(), pluginTokenSettingsKey, "not-valid-hex!!"); err != nil {
		t.Fatalf("Settings.Set: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/manifest", nil)
	_, ok := a.currentToken(req)
	if ok {
		t.Fatal("expected ok=false for a non-hex stored value")
	}
}

func TestCurrentTokenRejectsUndecryptableStoredValue(t *testing.T) {
	a := newTestApp(t)
	// Valid hex, but not real ciphertext for this encryptor - Decrypt fails.
	if err := a.Settings.Set(t.Context(), pluginTokenSettingsKey, "deadbeef"); err != nil {
		t.Fatalf("Settings.Set: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/manifest", nil)
	_, ok := a.currentToken(req)
	if ok {
		t.Fatal("expected ok=false for a stored value that fails to decrypt")
	}
}

// NOTE: Register's a.Encryptor.Encrypt(token) error branch (register.go
// line ~41) is not covered, and can't be via the same rand.Reader-swap
// trick internal/util's own crypto_randfail_test.go uses to reach
// Encrypt's nonce-generation failure directly: Register calls the
// package-level rand.Read(raw) FIRST (to generate the token itself,
// unconditionally, before ever reaching Encrypt), and forcing any
// rand.Reader failure now crashes the whole process instead of returning
// an error - crypto/rand.Read's doc says it never returns an error, and
// Go 1.24+ enforces that by calling a fatal, unrecoverable runtime abort
// on a real entropy failure rather than propagating one (confirmed
// empirically: swapping rand.Reader here to force Encrypt's error crashed
// this entire test binary via rand.Read, not Encrypt). So this branch is
// reachable in principle (Encrypt itself IS fully tested in isolation,
// see internal/util/crypto_randfail_test.go) but not from Register's own
// call site without a change to production code (e.g. splitting Register's
// two rand-consuming steps so only one can be faulted at a time), which
// isn't warranted just to reach one defensive line.

func TestRegisterReturnsServerErrorOnSetIfAbsentFailure(t *testing.T) {
	a := newTestApp(t)
	a.Settings = &models.SettingsStore{DB: brokenBTDB(t)}

	rec := httptest.NewRecorder()
	a.Register(rec, httptest.NewRequest(http.MethodPost, "/register", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", rec.Code, rec.Body.String())
	}
}
