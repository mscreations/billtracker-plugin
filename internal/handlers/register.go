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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/mscreations/billtracker-plugin/internal/logging"
)

// pluginTokenSettingsKey is the bt_settings row holding this plugin's
// shared token (encrypted at rest with a.Encryptor), issued exactly once
// via Register.
const pluginTokenSettingsKey = "plugin_token"

type registerResponse struct {
	Token string `json:"token"`
}

// Register handles POST /register - hhq's one-time self-registration call
// (see hhq's internal/plugins/register.go and internal/handlers/
// plugin_bootstrap.go for the host-side flow, and this repo's CLAUDE.md for
// the full picture). Generates a fresh token, persists it encrypted, and
// returns it in plaintext - but only the FIRST time this is ever called:
// SetIfAbsent makes that check-and-store atomic, so two racing callers
// (e.g. hhq retrying while a slow first response is still in flight) can
// never both "win", and a caller that loses the race gets 403 without ever
// learning the winning token. Deliberately unauthenticated - this is the
// one endpoint that must be reachable before any token exists yet (see
// RequireBearerToken's doc comment for why every other route is naturally
// unreachable until this succeeds).
func (a *App) Register(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	raw := make([]byte, 32)
	rand.Read(raw) // crypto/rand.Read never returns an error - see its doc comment
	token := hex.EncodeToString(raw)

	ciphertext, err := a.Encryptor.Encrypt(token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	inserted, err := a.Settings.SetIfAbsent(ctx, pluginTokenSettingsKey, hex.EncodeToString(ciphertext))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !inserted {
		http.Error(w, "already registered", http.StatusForbidden)
		return
	}

	logging.Infof("self-registered a new hhq plugin token")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(registerResponse{Token: token})
}
