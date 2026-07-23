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
)

type manifestView struct {
	Enabled bool   `json:"enabled"`
	Label   string `json:"label"`
	Icon    string `json:"icon"`
}

type manifest struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Version        string       `json:"version"`
	View           manifestView `json:"view"`
	ProvidesEvents bool         `json:"provides_events"`
}

const (
	pluginID   = "bill-tracker"
	pluginName = "Bill Tracker"

	// viewIcon is a small hand-rolled inline SVG (receipt/dollar icon),
	// trusted verbatim by hhq and rendered directly into the kiosk nav
	// button (see hhq's internal/plugins package doc for the trust
	// boundary this implies).
	viewIcon = `<svg class="nav-icon" viewBox="0 0 24 24" aria-hidden="true"><defs><linearGradient id="bt-receipt" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stop-color="#f0fdf4"/><stop offset="100%" stop-color="#86efac"/></linearGradient><linearGradient id="bt-dollar" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stop-color="#fde68a"/><stop offset="100%" stop-color="#d97706"/></linearGradient></defs><path d="M6 2h12a1 1 0 0 1 1 1v18l-2.5-1.5L14 21l-2-1.5L10 21l-2.5-1.5L5 21V3a1 1 0 0 1 1-1z" fill="url(#bt-receipt)"/><g stroke="#16a34a" stroke-width="1.4" stroke-linecap="round"><line x1="8.5" y1="7.5" x2="15.5" y2="7.5"/><line x1="8.5" y1="11" x2="15.5" y2="11"/><line x1="8.5" y1="14.5" x2="13" y2="14.5"/></g><text x="17" y="6.5" text-anchor="middle" font-size="6" font-weight="700" fill="url(#bt-dollar)">$</text></svg>`
)

// Manifest handles GET /manifest - a full-screen kiosk view is always
// enabled (no per-plugin layout configurability needed now that the view
// gets the whole screen, unlike the old column-constrained widget).
func (a *App) Manifest(w http.ResponseWriter, r *http.Request) {
	m := manifest{
		ID:      pluginID,
		Name:    pluginName,
		Version: a.Version,
		View: manifestView{
			Enabled: true,
			Label:   "Bills",
			Icon:    viewIcon,
		},
		ProvidesEvents: true,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m)
}
