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
	"strings"

	"github.com/mscreations/billtracker-plugin/internal/release"
)

type versionResponse struct {
	Version          string `json:"version"`
	UpgradeAvailable bool   `json:"upgradeAvailable"`
	UpgradeVersion   string `json:"upgradeVersion"`
	Changelog        string `json:"changelog"`
	Channel          string `json:"channel"`
	// Checked is false until the scheduler's first GitHub poll (see
	// internal/scheduler's runVersionCheck) has completed - lets hhq tell
	// "no update, confirmed" apart from "haven't looked yet" (e.g. right at
	// startup, before the first background check has finished) so it knows
	// whether to trust upgradeAvailable=false or come back and ask again soon.
	Checked bool `json:"checked"`
}

// Version handles GET /version - unauthenticated, like /healthz, since hhq
// polls this before (and independent of) self-registration. Reports this
// plugin's own currently-running version and, if the scheduler's periodic
// GitHub check (see internal/scheduler's runVersionCheck) has found a newer
// one, that too - hhq no longer needs to know this plugin's repo or talk to
// GitHub on its behalf, it just reads what this endpoint reports.
func (a *App) GetVersion(w http.ResponseWriter, r *http.Request) {
	channel := "release"
	if strings.HasSuffix(a.Version, "-dev") {
		channel = "dev"
	}

	resp := versionResponse{Version: a.Version, Channel: channel}
	if a.Releases != nil {
		if info, ok := a.Releases.Get(); ok {
			resp.Checked = true
			if release.IsNewer(a.Version, info.Version) {
				resp.UpgradeAvailable = true
				resp.UpgradeVersion = info.Version
				resp.Changelog = info.Changelog
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
