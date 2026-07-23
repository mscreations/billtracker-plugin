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
	"database/sql"
	"testing"
	"time"

	"github.com/mscreations/billtracker-plugin/internal/config"
	"github.com/mscreations/billtracker-plugin/internal/models"
)

func intPtr(n int) *int { return &n }

func TestBootstrapBillsCreatesMonthlyQuarterlyOneOffAndVendor(t *testing.T) {
	a := newFullTestApp(t)
	entries := []config.BillBootstrap{
		{Name: "Rent", Amount: 1500, Schedule: "monthly", DayOfMonth: intPtr(1)},
		{Name: "Insurance", Amount: 300, Schedule: "quarterly", DayOfMonth: intPtr(15), QuarterStartMonth: intPtr(2)},
		{Name: "Property Tax", Amount: 2200, Schedule: "one_off", OneOffDate: "2026-12-01"},
		{Name: "Electric", Connector: "nonexistent-connector", Username: "u", Password: "p"},
	}
	a.BootstrapBills(t.Context(), entries)

	defs, err := a.BillDefs.ListAll(t.Context())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(defs) != 4 {
		t.Fatalf("got %d bill defs, want 4: %+v", len(defs), defs)
	}
	for _, d := range defs {
		if !d.BootstrapManaged {
			t.Errorf("bill %q should be bootstrap-managed", d.Name)
		}
	}
}

func TestBootstrapBillsSkipsInvalidEntry(t *testing.T) {
	a := newFullTestApp(t)
	a.BootstrapBills(t.Context(), []config.BillBootstrap{
		{Name: "", Amount: 10, Schedule: "monthly", DayOfMonth: intPtr(1)}, // missing name
		{Name: "BadSchedule", Amount: 10, Schedule: "weekly"},
	})
	defs, err := a.BillDefs.ListAll(t.Context())
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("expected no bills created from invalid entries, got %+v", defs)
	}
}

func TestBootstrapBillsSkipsNameCollisionWithUICreatedBill(t *testing.T) {
	a := newFullTestApp(t)
	if _, err := a.BillDefs.Create(t.Context(), models.BillDefinition{
		Name: "Water", AmountCents: 100, ScheduleType: models.ScheduleOneOff,
		OneOffDate: sqlTime(t, "2026-01-01"),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	a.BootstrapBills(t.Context(), []config.BillBootstrap{
		{Name: "Water", Amount: 50, Schedule: "monthly", DayOfMonth: intPtr(1)},
	})

	def, err := a.BillDefs.GetByName(t.Context(), "Water")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if def.BootstrapManaged {
		t.Fatal("expected the UI-created bill to remain non-bootstrap-managed")
	}
	if def.AmountCents != 100 {
		t.Fatalf("AmountCents = %d, want unchanged 100", def.AmountCents)
	}
}

func TestBootstrapBillsUpdatesExistingBootstrapManagedBill(t *testing.T) {
	a := newFullTestApp(t)
	entries := []config.BillBootstrap{{Name: "Gas", Amount: 80, Schedule: "monthly", DayOfMonth: intPtr(10)}}
	a.BootstrapBills(t.Context(), entries)

	entries[0].Amount = 95
	entries[0].DayOfMonth = intPtr(12)
	a.BootstrapBills(t.Context(), entries)

	def, err := a.BillDefs.GetByName(t.Context(), "Gas")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if def.AmountCents != 9500 {
		t.Fatalf("AmountCents = %d, want 9500", def.AmountCents)
	}
	if !def.DayOfMonth.Valid || def.DayOfMonth.Int16 != 12 {
		t.Fatalf("DayOfMonth = %+v, want 12", def.DayOfMonth)
	}
}

func TestBootstrapBillsRemovesBootstrapManagedBillNoLongerInFile(t *testing.T) {
	a := newFullTestApp(t)
	a.BootstrapBills(t.Context(), []config.BillBootstrap{
		{Name: "Old Bill", Amount: 10, Schedule: "monthly", DayOfMonth: intPtr(1)},
	})
	if _, err := a.BillDefs.GetByName(t.Context(), "Old Bill"); err != nil {
		t.Fatalf("expected Old Bill to exist: %v", err)
	}

	a.BootstrapBills(t.Context(), []config.BillBootstrap{})

	if _, err := a.BillDefs.GetByName(t.Context(), "Old Bill"); err != models.ErrBillNotFound {
		t.Fatalf("expected Old Bill to be removed, err=%v", err)
	}
}

func TestBootstrapBillsAttachesVendorConnection(t *testing.T) {
	a := newFullTestApp(t)
	a.BootstrapBills(t.Context(), []config.BillBootstrap{
		{Name: "Electric", Connector: "some-connector", Tenant: "t1", Username: "user", Password: "pass"},
	})

	def, err := a.BillDefs.GetByName(t.Context(), "Electric")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	conn, err := a.Vendors.GetByBillDefinitionID(t.Context(), def.ID)
	if err != nil {
		t.Fatalf("GetByBillDefinitionID: %v", err)
	}
	if conn.Connector != "some-connector" || conn.Username != "user" {
		t.Fatalf("unexpected vendor connection: %+v", conn)
	}
}

func TestBootstrapBillsVendorEntryMissingCredentialsSkipped(t *testing.T) {
	a := newFullTestApp(t)
	a.BootstrapBills(t.Context(), []config.BillBootstrap{
		{Name: "NoPass", Connector: "some-connector", Username: "user"},
	})

	def, err := a.BillDefs.GetByName(t.Context(), "NoPass")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if _, err := a.Vendors.GetByBillDefinitionID(t.Context(), def.ID); err == nil {
		t.Fatal("expected no vendor connection to be attached without credentials")
	}
}

func TestBootstrapBillsListAllFailureForRemovalPassIsNonFatal(t *testing.T) {
	a := newFullTestApp(t)
	a.BillDefs = &models.BillDefinitionStore{DB: brokenBTDB(t)}
	// Must not panic even though every DB call in this pass will fail.
	a.BootstrapBills(t.Context(), []config.BillBootstrap{{Name: "X", Amount: 1, Schedule: "monthly", DayOfMonth: intPtr(1)}})
}

func sqlTime(t *testing.T, s string) sql.NullTime {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return sql.NullTime{Time: parsed, Valid: true}
}
