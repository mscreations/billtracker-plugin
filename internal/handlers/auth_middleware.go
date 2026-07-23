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
func (a *App) RequireBearerToken(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := a.currentToken(r)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// currentToken decrypts and returns this plugin's stored shared token, or
// ok=false if it hasn't self-registered with hhq yet.
func (a *App) currentToken(r *http.Request) (token string, ok bool) {
	stored, err := a.Settings.Get(r.Context(), pluginTokenSettingsKey, "")
	if err != nil || stored == "" {
		return "", false
	}
	ciphertext, err := hex.DecodeString(stored)
	if err != nil {
		return "", false
	}
	plaintext, err := a.Encryptor.Decrypt(ciphertext)
	if err != nil {
		return "", false
	}
	return plaintext, true
}
