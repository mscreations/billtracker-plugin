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
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/mscreations/billtracker-plugin/internal/logging"
)

// pluginTokenSettingsKey is the bt_settings row holding this plugin's
// shared token (encrypted at rest with a.Encryptor), issued/reissued via
// Register.
const pluginTokenSettingsKey = "plugin_token"

// connectionSecretHeader carries the shared connection secret hhq presents
// on every POST /register call (see config.Config.PluginConnectionSecret
// and hhq's internal/plugins/register.go) - this is what protects /register
// now that it's callable more than once per plugin lifetime.
const connectionSecretHeader = "X-Plugin-Connection-Secret"

type registerResponse struct {
	Token string `json:"token"`
}

// Register handles POST /register - hhq's self-registration call (see
// hhq's internal/plugins/register.go and internal/handlers/
// plugin_bootstrap.go for the host-side flow, and this repo's CLAUDE.md for
// the full picture). Requires a valid X-Plugin-Connection-Secret header
// matching a.Cfg.PluginConnectionSecret (constant-time compared) - a
// mismatch or missing header is rejected with **401 Unauthorized**,
// deliberately distinct from the 403 every other route uses for a stale
// bearer token (see RequireBearerToken): a bad connection secret is an
// operator misconfiguration (PLUGIN_CONNECTION_SECRET differs between hhq
// and this plugin) that hhq can't fix by retrying, so hhq's
// internal/plugins.Register recognizes 401 specifically and surfaces a
// pointed "check that PLUGIN_CONNECTION_SECRET matches" message instead of
// a generic error. On a valid secret, generates a fresh token, persists it
// encrypted (overwriting whatever was stored before), and returns it in
// plaintext: unlike the original one-time-only design, a valid secret lets
// this succeed every time, which is what lets hhq recover automatically
// after a 403 from any other route - the secret, not "first caller wins,"
// is now what protects this endpoint.
func (a *App) Register(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	got := r.Header.Get(connectionSecretHeader)
	if subtle.ConstantTimeCompare([]byte(got), []byte(a.Cfg.PluginConnectionSecret)) != 1 {
		// Never log the secret itself (got may be attacker-controlled, and
		// even a legitimate mismatch shouldn't end up in logs verbatim) -
		// just that a mismatch happened, and from where.
		logging.Warnf("rejected /register: connection secret mismatch or missing (from %s)", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	raw := make([]byte, 32)
	rand.Read(raw) // crypto/rand.Read never returns an error - see its doc comment
	token := hex.EncodeToString(raw)

	ciphertext, err := a.Encryptor.Encrypt(token)
	if err != nil {
		logging.Errorf("register: encrypting issued token: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := a.Settings.Set(ctx, pluginTokenSettingsKey, hex.EncodeToString(ciphertext)); err != nil {
		logging.Errorf("register: storing issued token: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	logging.Infof("issued a new hhq plugin token (from %s)", r.RemoteAddr)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(registerResponse{Token: token})
}
