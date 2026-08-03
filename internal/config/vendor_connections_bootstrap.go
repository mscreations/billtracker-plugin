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

package config

import (
	"encoding/json"
	"fmt"
)

// VendorConnectionBootstrap is one entry in the VENDOR_CONNECTIONS (or
// VENDOR_CONNECTIONS_FILE) JSON array. BillName must match an existing
// bt_bill_definitions.name exactly (created either via bills.json or the
// settings UI) - this only attaches vendor-portal login info to a bill that
// already exists, it does not create bills. Deliberately no per-entry
// validation here, same rationale as bills_bootstrap.go: one malformed
// entry shouldn't fail the whole file.
//
// Credentials are plaintext in this struct/file by necessity, same as any
// bootstrap secret - VENDOR_CONNECTIONS_FILE is meant to point at a
// Kubernetes Secret-mounted file (via the existing config.Getenv _FILE
// convention), not a plaintext ConfigMap, mirroring hhq's
// CALENDAR_ACCOUNTS_FILE bootstrap. PasswordFile, if set instead of
// Password, is a path to a file whose contents are the password - see
// BillBootstrap.PasswordFile/ResolvePassword for the same convention.
type VendorConnectionBootstrap struct {
	BillName     string `json:"bill_name"`
	Connector    string `json:"connector"` // registry key, e.g. "billeriq"
	Tenant       string `json:"tenant"`
	Username     string `json:"username"`
	UsernameFile string `json:"username_file,omitempty"`
	Password     string `json:"password"`
	PasswordFile string `json:"password_file,omitempty"`
}

// ResolvePassword returns the entry's effective password: Password if set,
// or the trimmed contents of PasswordFile if that's set instead. Returns an
// error if both are set (ambiguous) or if PasswordFile can't be read.
func (e VendorConnectionBootstrap) ResolvePassword() (string, error) {
	return resolveBootstrapField("password", e.Password, e.PasswordFile)
}

// ResolveUsername returns the entry's effective username: Username if set,
// or the trimmed contents of UsernameFile if that's set instead. Same
// mutual-exclusion/error behavior as ResolvePassword.
func (e VendorConnectionBootstrap) ResolveUsername() (string, error) {
	return resolveBootstrapField("username", e.Username, e.UsernameFile)
}

// ParseVendorConnectionsBootstrap parses the raw contents of the
// VENDOR_CONNECTIONS(_FILE) value.
func ParseVendorConnectionsBootstrap(raw string) ([]VendorConnectionBootstrap, error) {
	if raw == "" {
		return nil, nil
	}
	var entries []VendorConnectionBootstrap
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("parsing VENDOR_CONNECTIONS: %w", err)
	}
	return entries, nil
}
