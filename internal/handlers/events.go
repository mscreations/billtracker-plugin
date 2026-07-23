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
	"fmt"
	"net/http"
	"time"
)

type eventActionOut struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	RequiresParent bool   `json:"requires_parent,omitempty"`
}

type eventOut struct {
	UID         string           `json:"uid"`
	Summary     string           `json:"summary"`
	Location    string           `json:"location"`
	Description string           `json:"description"`
	StartsAt    time.Time        `json:"starts_at"`
	EndsAt      time.Time        `json:"ends_at"`
	AllDay      bool             `json:"all_day"`
	Actions     []eventActionOut `json:"actions,omitempty"`
}

// markPaidActions is reused for every unpaid bill event - hhq surfaces this
// as a button on the kiosk's event detail popup; tapping it POSTs to
// /actions/mark_paid (see Action below). RequiresParent is set so hhq only
// shows/allows the button to a signed-in parent - a child tapping around an
// unattended kiosk shouldn't be able to mark a bill paid by accident.
var markPaidActions = []eventActionOut{{ID: "mark_paid", Label: "Mark Paid", RequiresParent: true}}

// Events handles GET /events?from=YYYY-MM-DD&to=YYYY-MM-DD - synthetic
// calendar events for unpaid bills due in the window, polled periodically by
// hhq's scheduler (see internal/plugins.FetchEvents on the hhq side).
func (a *App) Events(w http.ResponseWriter, r *http.Request) {
	// "from" is validated but intentionally unused below: an unpaid bill
	// stays relevant indefinitely until paid, so ListUpcomingUnpaid has no
	// lower bound (see its doc comment) - bounding results to "from" would
	// silently exclude exactly the overdue bills the kiosk needs to show.
	if _, err := time.Parse("2006-01-02", r.URL.Query().Get("from")); err != nil {
		http.Error(w, "from/to query params must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}
	to, err2 := time.Parse("2006-01-02", r.URL.Query().Get("to"))
	if err2 != nil {
		http.Error(w, "from/to query params must be YYYY-MM-DD", http.StatusBadRequest)
		return
	}

	instances, err := a.Instances.ListUpcomingUnpaid(r.Context(), to)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	events := make([]eventOut, 0, len(instances))
	for _, inst := range instances {
		// inst.DueDate is a DATE column - pgx round-trips it tagged UTC
		// regardless of how it was written, but it carries no real
		// timezone, just a calendar day. Reinterpret its Y/M/D in
		// time.Local (the cluster-wide TZ) so the instant sent to hhq
		// actually represents local midnight of that day, not UTC midnight
		// - otherwise a bill due "today" locally can land on the wrong
		// side of hhq's local-day boundary and show as due a day early.
		due := time.Date(inst.DueDate.Year(), inst.DueDate.Month(), inst.DueDate.Day(), 0, 0, 0, 0, time.Local)
		events = append(events, eventOut{
			UID:      fmt.Sprintf("bill-instance-%d", inst.InstanceID),
			Summary:  fmt.Sprintf("%s bill due (%s)", inst.Name, formatCents(inst.AmountCents)),
			StartsAt: due,
			EndsAt:   due.Add(24 * time.Hour),
			AllDay:   true,
			Actions:  markPaidActions,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"events": events})
}

type actionIn struct {
	UID string `json:"uid"`
}

// Action handles POST /actions/{id} - hhq's kiosk-facing callback for a
// plugin-declared event action button (see markPaidActions above and
// internal/plugins.PostAction on the hhq side). Unlike the settings page's
// markPaid (settings.go), which is reached only through hhq's
// parent-authenticated ProxySettings, this route is called directly by hhq
// on behalf of a kiosk tap - it's scoped tightly (only "mark_paid" is
// recognized, and only ever applied to the specific bill instance encoded in
// the event's own uid) rather than a general write API. Whether the tap
// itself required a signed-in parent is enforced entirely on hhq's side (see
// markPaidActions' RequiresParent field and hhq's KioskEventAction handler) -
// this plugin has no session/login concept of its own and trusts hhq's bearer
// token as the full authentication boundary for this route.
func (a *App) Action(w http.ResponseWriter, r *http.Request) {
	actionID := r.PathValue("id")
	if actionID != "mark_paid" {
		http.NotFound(w, r)
		return
	}

	var in actionIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	var instanceID int
	if _, err := fmt.Sscanf(in.UID, "bill-instance-%d", &instanceID); err != nil {
		http.Error(w, "unrecognized event uid", http.StatusBadRequest)
		return
	}

	if err := a.Instances.MarkPaid(r.Context(), instanceID); err != nil {
		http.Error(w, "marking paid: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// Healthz handles GET /healthz.
func (a *App) Healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
