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

func TestParseBillsBootstrapEmptyIsNoOp(t *testing.T) {
	entries, err := ParseBillsBootstrap("")
	if err != nil {
		t.Fatalf("ParseBillsBootstrap(\"\"): %v", err)
	}
	if entries != nil {
		t.Errorf("entries = %v, want nil", entries)
	}
}

func TestParseBillsBootstrapValidJSON(t *testing.T) {
	raw := `[
		{"name": "Electric", "amount": 45.67, "schedule": "monthly", "day_of_month": 15},
		{"name": "Water", "connector": "billeriq", "tenant": "WVWAuthority", "username": "u", "password": "p"}
	]`
	entries, err := ParseBillsBootstrap(raw)
	if err != nil {
		t.Fatalf("ParseBillsBootstrap: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].Name != "Electric" || entries[0].Amount != 45.67 || entries[0].Schedule != "monthly" {
		t.Errorf("entries[0] = %+v", entries[0])
	}
	if entries[0].DayOfMonth == nil || *entries[0].DayOfMonth != 15 {
		t.Errorf("entries[0].DayOfMonth = %v, want 15", entries[0].DayOfMonth)
	}
	if entries[1].Connector != "billeriq" || entries[1].Tenant != "WVWAuthority" || entries[1].Username != "u" || entries[1].Password != "p" {
		t.Errorf("entries[1] = %+v", entries[1])
	}
}

func TestParseBillsBootstrapInvalidJSON(t *testing.T) {
	_, err := ParseBillsBootstrap("not json at all")
	if err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}

func TestParseBillsBootstrapEmptyArrayIsEmptyNotNil(t *testing.T) {
	entries, err := ParseBillsBootstrap("[]")
	if err != nil {
		t.Fatalf("ParseBillsBootstrap(\"[]\"): %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("len(entries) = %d, want 0", len(entries))
	}
}
