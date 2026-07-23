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

import "testing"

func TestParseVendorConnectionsBootstrapEmptyIsNoOp(t *testing.T) {
	entries, err := ParseVendorConnectionsBootstrap("")
	if err != nil {
		t.Fatalf("ParseVendorConnectionsBootstrap(\"\"): %v", err)
	}
	if entries != nil {
		t.Errorf("entries = %v, want nil", entries)
	}
}

func TestParseVendorConnectionsBootstrapValidJSON(t *testing.T) {
	raw := `[
		{"bill_name": "Electric", "connector": "billeriq", "tenant": "WVWAuthority", "username": "u", "password": "p"},
		{"bill_name": "Gas", "connector": "columbiagaspa", "username": "u2", "password": "p2"}
	]`
	entries, err := ParseVendorConnectionsBootstrap(raw)
	if err != nil {
		t.Fatalf("ParseVendorConnectionsBootstrap: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].BillName != "Electric" || entries[0].Connector != "billeriq" || entries[0].Tenant != "WVWAuthority" {
		t.Errorf("entries[0] = %+v", entries[0])
	}
	if entries[1].BillName != "Gas" || entries[1].Connector != "columbiagaspa" || entries[1].Username != "u2" {
		t.Errorf("entries[1] = %+v", entries[1])
	}
}

func TestParseVendorConnectionsBootstrapInvalidJSON(t *testing.T) {
	_, err := ParseVendorConnectionsBootstrap("not json at all")
	if err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

func TestParseVendorConnectionsBootstrapEmptyArrayIsEmptyNotNil(t *testing.T) {
	entries, err := ParseVendorConnectionsBootstrap("[]")
	if err != nil {
		t.Fatalf("ParseVendorConnectionsBootstrap(\"[]\"): %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("len(entries) = %d, want 0", len(entries))
	}
}
