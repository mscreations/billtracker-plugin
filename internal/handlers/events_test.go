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
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mscreations/billtracker-plugin/internal/models"
)

func createTestBill(t *testing.T, a *App, name string, dueDate time.Time) (defID, instID int) {
	t.Helper()
	id, err := a.BillDefs.Create(t.Context(), models.BillDefinition{
		Name:         name,
		AmountCents:  1234,
		ScheduleType: models.ScheduleOneOff,
		OneOffDate:   sql.NullTime{Time: dueDate, Valid: true},
	})
	if err != nil {
		t.Fatalf("BillDefs.Create: %v", err)
	}
	if err := a.Instances.EnsureInstance(t.Context(), id, dueDate); err != nil {
		t.Fatalf("EnsureInstance: %v", err)
	}
	instances, err := a.Instances.ListAllForSettings(t.Context(), dueDate.AddDate(0, 0, -1), dueDate.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("ListAllForSettings: %v", err)
	}
	for _, inst := range instances {
		if inst.DefinitionID == id {
			return id, inst.InstanceID
		}
	}
	t.Fatal("could not find created instance")
	return 0, 0
}

func TestEventsRejectsMissingOrMalformedDates(t *testing.T) {
	a := newFullTestApp(t)

	for _, tc := range []struct {
		name  string
		query string
	}{
		{"missing both", ""},
		{"missing from", "?to=2026-01-01"},
		{"malformed from", "?from=not-a-date&to=2026-01-01"},
		{"missing to", "?from=2026-01-01"},
		{"malformed to", "?from=2026-01-01&to=not-a-date"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/events"+tc.query, nil)
			rec := httptest.NewRecorder()
			a.Events(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestEventsReturnsUnpaidBillsAsEvents(t *testing.T) {
	a := newFullTestApp(t)
	due := time.Now().AddDate(0, 0, 3)
	createTestBill(t, a, "Electric", due)

	req := httptest.NewRequest(http.MethodGet, "/events?from=2020-01-01&to="+due.AddDate(0, 0, 5).Format("2006-01-02"), nil)
	rec := httptest.NewRecorder()
	a.Events(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Events []eventOut `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(out.Events) != 1 {
		t.Fatalf("got %d events, want 1", len(out.Events))
	}
	if !strings.Contains(out.Events[0].Summary, "Electric") {
		t.Errorf("Summary = %q, want it to mention the bill name", out.Events[0].Summary)
	}
	if len(out.Events[0].Actions) != 1 || out.Events[0].Actions[0].ID != "mark_paid" {
		t.Errorf("Actions = %+v, want a single mark_paid action", out.Events[0].Actions)
	}
}

func TestEventsReturnsServerErrorOnDBFailure(t *testing.T) {
	a := newFullTestApp(t)
	a.Instances = &models.BillInstanceStore{DB: brokenBTDB(t)}

	req := httptest.NewRequest(http.MethodGet, "/events?from=2020-01-01&to=2020-01-02", nil)
	rec := httptest.NewRecorder()
	a.Events(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestActionRejectsUnknownActionID(t *testing.T) {
	a := newFullTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/actions/not_mark_paid", bytes.NewReader([]byte(`{"uid":"bill-instance-1"}`)))
	req.SetPathValue("id", "not_mark_paid")
	rec := httptest.NewRecorder()
	a.Action(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestActionRejectsMalformedBody(t *testing.T) {
	a := newFullTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/actions/mark_paid", bytes.NewReader([]byte(`not json`)))
	req.SetPathValue("id", "mark_paid")
	rec := httptest.NewRecorder()
	a.Action(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestActionRejectsUnrecognizedUID(t *testing.T) {
	a := newFullTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/actions/mark_paid", bytes.NewReader([]byte(`{"uid":"not-a-bill-instance-uid"}`)))
	req.SetPathValue("id", "mark_paid")
	rec := httptest.NewRecorder()
	a.Action(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestActionMarksBillPaid(t *testing.T) {
	a := newFullTestApp(t)
	_, instID := createTestBill(t, a, "Water", time.Now().AddDate(0, 0, 2))

	body := []byte(`{"uid":"bill-instance-` + strconv.Itoa(instID) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/actions/mark_paid", bytes.NewReader(body))
	req.SetPathValue("id", "mark_paid")
	rec := httptest.NewRecorder()
	a.Action(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	instances, err := a.Instances.ListUpcomingUnpaid(t.Context(), time.Now().AddDate(0, 1, 0))
	if err != nil {
		t.Fatalf("ListUpcomingUnpaid: %v", err)
	}
	for _, inst := range instances {
		if inst.InstanceID == instID {
			t.Fatal("expected the instance to no longer be unpaid")
		}
	}
}

func TestActionReturnsServerErrorOnMarkPaidFailure(t *testing.T) {
	a := newFullTestApp(t)
	// A well-formed uid pointing at an instance id that doesn't exist -
	// MarkPaid errors ("not found") rather than silently no-op'ing.
	body := []byte(`{"uid":"bill-instance-999999"}`)
	req := httptest.NewRequest(http.MethodPost, "/actions/mark_paid", bytes.NewReader(body))
	req.SetPathValue("id", "mark_paid")
	rec := httptest.NewRecorder()
	a.Action(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHealthzReturnsOK(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	a.Healthz(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestManifestReturnsExpectedShape(t *testing.T) {
	a := &App{}
	req := httptest.NewRequest(http.MethodGet, "/manifest", nil)
	rec := httptest.NewRecorder()
	a.Manifest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var m manifest
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if m.ID != pluginID || !m.View.Enabled || !m.ProvidesEvents {
		t.Errorf("unexpected manifest shape: %+v", m)
	}
}

// brokenBTDB returns an open-then-closed *sql.DB so queries against it
// deterministically fail with "sql: database is closed" - mirrors the same
// technique used throughout hhq's own coverage work (see d:\hhq\cmd\server's
// brokenDB), reimplemented here for billtracker's own DSN shape.
func brokenBTDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", "postgres://broken:broken@127.0.0.1:1/broken?sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.Close()
	t.Cleanup(func() { db.Close() })
	return db
}
