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

// BillBootstrap is one entry in bills.json. Amount is dollars (as written by
// a human editing the file); handlers.BootstrapBills converts it to cents.
// Deliberately no per-entry validation here - same rationale as hhq's
// calendar_bootstrap.go: one malformed entry shouldn't fail the whole file,
// so validation happens later in the reconciliation loop where a single bad
// entry can be skipped and logged instead.
//
// If Connector is set, Amount/Schedule/DayOfMonth/QuarterStartMonth/
// OneOffDate are all ignored - the bill's due date and amount are entirely
// maintained by the scheduler's vendor-refresh job instead (see
// internal/connectors and internal/models.ScheduleVendor). Tenant/Username/
// Password are that connector's login; Tenant's meaning is
// connector-specific (e.g. billeriq's per-utility URL path segment).
// Because this puts a plaintext password in bills.json, CONFIG_DIR should
// point at a Kubernetes Secret-mounted volume (not a plain ConfigMap) for
// any bills.json that has a connector-managed entry.
type BillBootstrap struct {
	Name              string  `json:"name"`
	Amount            float64 `json:"amount,omitempty"`
	Schedule          string  `json:"schedule,omitempty"` // "monthly", "quarterly", or "one_off"
	DayOfMonth        *int    `json:"day_of_month,omitempty"`
	QuarterStartMonth *int    `json:"quarter_start_month,omitempty"` // 1=Jan/Apr/Jul/Oct, 2=Feb/May/Aug/Nov, 3=Mar/Jun/Sep/Dec; required iff schedule is "quarterly"
	OneOffDate        string  `json:"one_off_date,omitempty"`        // YYYY-MM-DD
	VendorURL         string  `json:"vendor_url,omitempty"`

	Connector string `json:"connector,omitempty"` // internal/connectors registry key, e.g. "billeriq"
	Tenant    string `json:"tenant,omitempty"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
}

// ParseBillsBootstrap parses the raw contents of bills.json.
func ParseBillsBootstrap(raw string) ([]BillBootstrap, error) {
	if raw == "" {
		return nil, nil
	}
	var entries []BillBootstrap
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, fmt.Errorf("parsing bills.json: %w", err)
	}
	return entries, nil
}
