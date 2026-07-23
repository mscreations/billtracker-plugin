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
	"testing"

	"github.com/mscreations/billtracker-plugin/internal/config"
	"github.com/mscreations/billtracker-plugin/internal/models"
)

func TestBootstrapVendorConnectionsEmptyIsNoOp(t *testing.T) {
	a := newFullTestApp(t)
	a.BootstrapVendorConnections(t.Context(), nil)
	conns, err := a.Vendors.ListAll(t.Context())
	if err != nil || len(conns) != 0 {
		t.Fatalf("ListAll: %+v %v", conns, err)
	}
}

func TestBootstrapVendorConnectionsRequiresAllFields(t *testing.T) {
	a := newFullTestApp(t)
	a.BootstrapVendorConnections(t.Context(), []config.VendorConnectionBootstrap{
		{BillName: "", Connector: "c", Username: "u", Password: "p"},
		{BillName: "b", Connector: "", Username: "u", Password: "p"},
		{BillName: "b", Connector: "c", Username: "", Password: "p"},
		{BillName: "b", Connector: "c", Username: "u", Password: ""},
	})
	conns, err := a.Vendors.ListAll(t.Context())
	if err != nil || len(conns) != 0 {
		t.Fatalf("expected no vendor connections, got: %+v %v", conns, err)
	}
}

func TestBootstrapVendorConnectionsRejectsUnknownBill(t *testing.T) {
	a := newFullTestApp(t)
	a.BootstrapVendorConnections(t.Context(), []config.VendorConnectionBootstrap{
		{BillName: "Nonexistent", Connector: "c", Username: "u", Password: "p"},
	})
	conns, err := a.Vendors.ListAll(t.Context())
	if err != nil || len(conns) != 0 {
		t.Fatalf("expected no vendor connections, got: %+v %v", conns, err)
	}
}

func TestBootstrapVendorConnectionsAttachesToExistingBill(t *testing.T) {
	a := newFullTestApp(t)
	id, err := a.BillDefs.Create(t.Context(), models.BillDefinition{
		Name: "Water", AmountCents: 100, ScheduleType: models.ScheduleVendor,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	a.BootstrapVendorConnections(t.Context(), []config.VendorConnectionBootstrap{
		{BillName: "Water", Connector: "some-connector", Tenant: "t1", Username: "u", Password: "p"},
	})

	conn, err := a.Vendors.GetByBillDefinitionID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByBillDefinitionID: %v", err)
	}
	if conn.Connector != "some-connector" || conn.Username != "u" {
		t.Fatalf("unexpected connection: %+v", conn)
	}
}

func TestBootstrapVendorConnectionsGetByNameFailureIsNonFatal(t *testing.T) {
	a := newFullTestApp(t)
	a.BillDefs = &models.BillDefinitionStore{DB: brokenBTDB(t)}
	// Must not panic even though the lookup fails.
	a.BootstrapVendorConnections(t.Context(), []config.VendorConnectionBootstrap{
		{BillName: "Water", Connector: "c", Username: "u", Password: "p"},
	})
}
