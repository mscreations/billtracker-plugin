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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mscreations/billtracker-plugin/internal/config"
	"github.com/mscreations/billtracker-plugin/internal/models"
	"github.com/mscreations/billtracker-plugin/internal/testutil"
	"github.com/mscreations/billtracker-plugin/internal/util"
)

// testConnectionSecret is the value newTestApp configures as
// Cfg.PluginConnectionSecret - tests that need a valid POST /register call
// use registerRequest, which stamps this onto the request header.
const testConnectionSecret = "test-connection-secret"

func newTestApp(t *testing.T) *App {
	t.Helper()
	conn := testutil.RequireDB(t)
	encryptor, err := util.NewEncryptor(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	return &App{
		Cfg:       &config.Config{PluginConnectionSecret: testConnectionSecret},
		Settings:  &models.SettingsStore{DB: conn},
		Encryptor: encryptor,
	}
}

// registerRequest builds a POST /register request carrying the correct
// connection secret header (see testConnectionSecret).
func registerRequest() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/register", nil)
	req.Header.Set(connectionSecretHeader, testConnectionSecret)
	return req
}

// TestRegisterRejectsMissingOrWrongConnectionSecret is a direct regression
// test for the secret gate in front of /register (see register.go): a
// missing or incorrect X-Plugin-Connection-Secret header must be rejected
// with 401 (not 403 - that status is reserved for a stale bearer token on
// every other route, which triggers hhq's automatic re-registration; a bad
// connection secret is a standing misconfiguration hhq needs to surface
// distinctly instead, see hhq's internal/plugins.ErrConnectionSecretMismatch)
// before any token is touched.
func TestRegisterRejectsMissingOrWrongConnectionSecret(t *testing.T) {
	a := newTestApp(t)

	for _, tc := range []struct {
		name   string
		secret string
	}{
		{"missing header", ""},
		{"wrong secret", "not-the-real-secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/register", nil)
			if tc.secret != "" {
				req.Header.Set(connectionSecretHeader, tc.secret)
			}
			rec := httptest.NewRecorder()
			a.Register(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestRegisterReissuesATokenOnEveryValidCall is a direct regression test for
// the always-reissue behavior (see register.go): unlike the old one-time-
// only design, a valid connection secret must succeed on every call,
// returning a fresh token each time and overwriting the previous one - this
// is what lets hhq recover automatically after a 403 elsewhere.
func TestRegisterReissuesATokenOnEveryValidCall(t *testing.T) {
	a := newTestApp(t)

	rec := httptest.NewRecorder()
	a.Register(rec, registerRequest())
	if rec.Code != http.StatusOK {
		t.Fatalf("first /register status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var first registerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if first.Token == "" {
		t.Fatal("expected a non-empty token")
	}

	rec2 := httptest.NewRecorder()
	a.Register(rec2, registerRequest())
	if rec2.Code != http.StatusOK {
		t.Fatalf("second /register status = %d, want 200 (valid secret reissues); body: %s", rec2.Code, rec2.Body.String())
	}
	var second registerResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &second); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if second.Token == "" || second.Token == first.Token {
		t.Fatalf("expected a distinct fresh token on reissue, got %q (first was %q)", second.Token, first.Token)
	}

	// The old token must no longer authenticate - only the freshly issued one.
	handler := a.RequireBearerToken(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	oldReq := httptest.NewRequest(http.MethodGet, "/manifest", nil)
	oldReq.Header.Set("Authorization", "Bearer "+first.Token)
	oldRec := httptest.NewRecorder()
	handler(oldRec, oldReq)
	if oldRec.Code != http.StatusForbidden {
		t.Fatalf("old token status = %d, want 403 after reissue", oldRec.Code)
	}
}

// TestRequireBearerTokenRejectsUntilRegistered confirms every route wrapped
// in RequireBearerToken is naturally unreachable before self-registration
// has happened - no separate "not yet registered" flag is needed, since
// currentToken always returns ok=false until Register has stored something.
func TestRequireBearerTokenRejectsUntilRegistered(t *testing.T) {
	a := newTestApp(t)

	called := false
	handler := a.RequireBearerToken(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manifest", nil)
	req.Header.Set("Authorization", "Bearer whatever-anyone-might-guess")
	handler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 before registration", rec.Code)
	}
	if called {
		t.Fatal("the wrapped handler must not run before registration")
	}
}

// TestRequireBearerTokenAcceptsCorrectTokenAfterRegistration and its sibling
// below are direct regression tests for the transport mechanism every other
// endpoint relies on: correct token in, handler runs; wrong or missing
// token, 403 and the handler never runs.
func TestRequireBearerTokenAcceptsCorrectTokenAfterRegistration(t *testing.T) {
	a := newTestApp(t)

	rec := httptest.NewRecorder()
	a.Register(rec, registerRequest())
	var resp registerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding register response: %v", err)
	}

	called := false
	handler := a.RequireBearerToken(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/manifest", nil)
	req.Header.Set("Authorization", "Bearer "+resp.Token)
	rec2 := httptest.NewRecorder()
	handler(rec2, req)

	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with the correct token; body: %s", rec2.Code, rec2.Body.String())
	}
	if !called {
		t.Fatal("expected the wrapped handler to run with the correct token")
	}
}

func TestRequireBearerTokenRejectsWrongTokenAfterRegistration(t *testing.T) {
	a := newTestApp(t)

	rec := httptest.NewRecorder()
	a.Register(rec, registerRequest())
	var resp registerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding register response: %v", err)
	}

	called := false
	handler := a.RequireBearerToken(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	for _, tc := range []struct {
		name   string
		header string
	}{
		{"wrong token", "Bearer not-the-real-token"},
		{"no bearer prefix", resp.Token},
		{"missing header", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(http.MethodGet, "/manifest", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			handler(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}
			if called {
				t.Error("the wrapped handler must not run without a valid token")
			}
		})
	}
}
