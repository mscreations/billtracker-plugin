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

// Package connectors defines the interface vendor bill-pay portal
// integrations implement, plus a small registry keyed by connector slug
// (e.g. "billeriq"). A connector logs into a vendor's site on the bill's
// behalf and reports back the current due date/amount/account number -
// nothing here handles credential storage or scheduling, see
// internal/models.VendorConnectionStore and internal/scheduler for that.
package connectors

import (
	"context"
	"fmt"
	"time"
)

// Credentials is what a Connector needs to log in. Tenant is
// connector-specific (e.g. billeriq's per-utility URL path segment) and
// opaque outside the connector implementation.
type Credentials struct {
	Tenant   string
	Username string
	Password string
}

// BillSnapshot is the current state of a bill as reported by the vendor.
type BillSnapshot struct {
	AmountCents   int64
	DueDate       time.Time
	AccountNumber string
}

type Connector interface {
	// Slug identifies this connector in bt_vendor_connections.connector,
	// e.g. "billeriq".
	Slug() string
	FetchBill(ctx context.Context, creds Credentials) (BillSnapshot, error)
}

var registry = map[string]Connector{}

// Register adds a connector to the registry, keyed by its Slug(). Called
// from each connector package's init(), mirroring how database/sql drivers
// register themselves.
func Register(c Connector) {
	registry[c.Slug()] = c
}

// Get looks up a connector by slug (bt_vendor_connections.connector).
func Get(slug string) (Connector, error) {
	c, ok := registry[slug]
	if !ok {
		return nil, fmt.Errorf("no connector registered for %q", slug)
	}
	return c, nil
}
