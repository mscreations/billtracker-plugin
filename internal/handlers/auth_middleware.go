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
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/mscreations/billtracker-plugin/internal/logging"
)

// RequireBearerToken wraps h so every request must carry
// "Authorization: Bearer <token>" matching the token this plugin issued
// hhq on self-registration (see Register) - looked up fresh on every
// request rather than cached, since it doesn't exist yet at process start
// and this stays correct without a restart the moment registration
// completes. Until registration has happened, storedToken is always empty,
// so no request can ever satisfy this check - which is what keeps every
// other route disabled before self-registration, with no separate flag
// needed. Compared with subtle.ConstantTimeCompare so response timing
// can't be used to guess the token a byte at a time.
//
// Rejects with 403 Forbidden, not 401 - hhq treats a 403 from any of these
// routes as "my stored token no longer matches yours" and automatically
// re-registers (POST /register, with the shared connection secret) and
// retries once (see hhq's internal/handlers/plugin_auth.go). 401 would not
// trigger that recovery.
func (a *App) RequireBearerToken(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := a.currentToken(r)
		if !ok {
			// Routine/expected before this plugin has ever self-registered -
			// not worth a Warnf, but still useful at debug level when
			// tracing why every request 403s on a freshly started plugin.
			logging.Debugf("rejected %s %s: not yet registered with hhq", r.Method, r.URL.Path)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			// A real problem worth flagging once registration HAS happened -
			// either hhq's stored token has gone stale (which its own 403
			// handling should recover from automatically on the next
			// request) or something else entirely is hitting this port.
			// Never log the token itself.
			logging.Warnf("rejected %s %s: missing or mismatched bearer token (from %s)", r.Method, r.URL.Path, r.RemoteAddr)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

// currentToken decrypts and returns this plugin's stored shared token, or
// ok=false if it hasn't self-registered with hhq yet.
func (a *App) currentToken(r *http.Request) (token string, ok bool) {
	stored, err := a.Settings.Get(r.Context(), pluginTokenSettingsKey, "")
	if err != nil {
		logging.Errorf("currentToken: reading stored token: %v", err)
		return "", false
	}
	if stored == "" {
		return "", false
	}
	ciphertext, err := hex.DecodeString(stored)
	if err != nil {
		logging.Errorf("currentToken: stored token is not valid hex: %v", err)
		return "", false
	}
	plaintext, err := a.Encryptor.Decrypt(ciphertext)
	if err != nil {
		logging.Errorf("currentToken: decrypting stored token (wrong ENCRYPTION_KEY?): %v", err)
		return "", false
	}
	return plaintext, true
}
