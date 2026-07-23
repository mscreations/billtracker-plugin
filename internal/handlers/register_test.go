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

	"github.com/mscreations/billtracker-plugin/internal/models"
	"github.com/mscreations/billtracker-plugin/internal/testutil"
	"github.com/mscreations/billtracker-plugin/internal/util"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	conn := testutil.RequireDB(t)
	encryptor, err := util.NewEncryptor(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	return &App{
		Settings:  &models.SettingsStore{DB: conn},
		Encryptor: encryptor,
	}
}

// TestRegisterIssuesATokenOnce is a direct regression test for the
// self-registration handshake (see register.go): the first POST /register
// must return a usable token, and every subsequent call must be rejected
// with 403 rather than issuing (or leaking) a second one.
func TestRegisterIssuesATokenOnce(t *testing.T) {
	a := newTestApp(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/register", nil)
	a.Register(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("first /register status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var resp registerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("expected a non-empty token")
	}

	// A second registration attempt must fail - the plugin only ever answers
	// /register successfully once.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/register", nil)
	a.Register(rec2, req2)

	if rec2.Code != http.StatusForbidden {
		t.Fatalf("second /register status = %d, want 403; body: %s", rec2.Code, rec2.Body.String())
	}
	// The losing caller must never learn the winning token.
	if strings.Contains(rec2.Body.String(), resp.Token) {
		t.Fatal("the rejected second registration response leaked the first token")
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

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 before registration", rec.Code)
	}
	if called {
		t.Fatal("the wrapped handler must not run before registration")
	}
}

// TestRequireBearerTokenAcceptsCorrectTokenAfterRegistration and its sibling
// below are direct regression tests for the transport mechanism every other
// endpoint relies on: correct token in, handler runs; wrong or missing
// token, 401 and the handler never runs.
func TestRequireBearerTokenAcceptsCorrectTokenAfterRegistration(t *testing.T) {
	a := newTestApp(t)

	rec := httptest.NewRecorder()
	a.Register(rec, httptest.NewRequest(http.MethodPost, "/register", nil))
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
	a.Register(rec, httptest.NewRequest(http.MethodPost, "/register", nil))
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

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", rec.Code)
			}
			if called {
				t.Error("the wrapped handler must not run without a valid token")
			}
		})
	}
}
