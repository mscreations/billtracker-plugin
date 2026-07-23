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
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mscreations/billtracker-plugin/internal/config"
	"github.com/mscreations/billtracker-plugin/internal/logging"
	"github.com/mscreations/billtracker-plugin/internal/models"
	"github.com/mscreations/billtracker-plugin/internal/scheduler"
)

// BootstrapBills reconciles bt_bill_definitions against bills.json entries:
// creates missing ones, refreshes existing bootstrap-managed ones to match
// the file, skips (logs) name collisions with UI-created bills, and -
// mirroring hhq's BootstrapCalendarAccounts - deletes any bootstrap-managed
// definition whose name is no longer present in the file. An entry with a
// "connector" field also gets its bt_vendor_connections row attached/
// refreshed here (see attachVendorConnection) - one file, one pass.
func (a *App) BootstrapBills(ctx context.Context, entries []config.BillBootstrap) {
	seen := make(map[string]bool, len(entries))
	sched := &scheduler.Scheduler{Cfg: a.Cfg, BillDefs: a.BillDefs, Instances: a.Instances, Vendors: a.Vendors, Encryptor: a.Encryptor}

	for _, entry := range entries {
		def, err := billBootstrapToDefinition(entry)
		if err != nil {
			logging.Errorf("bootstrap: skipping bill %q: %v", entry.Name, err)
			continue
		}
		seen[def.Name] = true

		existing, err := a.BillDefs.GetByName(ctx, def.Name)
		switch {
		case err == models.ErrBillNotFound:
			def.BootstrapManaged = true
			id, err := a.BillDefs.Create(ctx, def)
			if err != nil {
				logging.Errorf("bootstrap: creating bill %q: %v", def.Name, err)
				continue
			}
			def.ID = id
			logging.Infof("bootstrap: created bill %q from bills.json", def.Name)
		case err != nil:
			logging.Errorf("bootstrap: looking up bill %q: %v", def.Name, err)
			continue
		case !existing.BootstrapManaged:
			logging.Warnf("bootstrap: bill %q already exists and was created via the settings UI - skipping bills.json entry", def.Name)
			continue
		default:
			def.ID = existing.ID
			if entry.Connector != "" {
				// Don't clobber the vendor-refreshed amount with the
				// zero-value placeholder billBootstrapToDefinition set for
				// a connector entry - attachVendorConnection below
				// re-fetches the real amount immediately after, but if
				// that fetch fails (e.g. vendor site down), the bill
				// should keep showing its last known-good amount rather
				// than a momentary $0.00.
				def.AmountCents = existing.AmountCents
			}
			if err := a.BillDefs.UpdateBootstrap(ctx, existing.ID, def); err != nil {
				logging.Errorf("bootstrap: updating bill %q: %v", def.Name, err)
				continue
			}
		}

		if entry.Connector != "" {
			a.attachVendorConnection(ctx, sched, def.ID, entry)
			continue
		}

		if err := scheduler.GenerateInstancesForDefinition(ctx, a.Instances, def, a.Cfg.BillInstanceLookaheadDays); err != nil {
			logging.Errorf("bootstrap: generating instances for %q: %v", def.Name, err)
		}
	}

	all, err := a.BillDefs.ListAll(ctx)
	if err != nil {
		logging.Errorf("bootstrap: listing bills for removal pass: %v", err)
		return
	}
	for _, d := range all {
		if d.BootstrapManaged && !seen[d.Name] {
			if err := a.BillDefs.Delete(ctx, d.ID); err != nil {
				logging.Errorf("bootstrap: removing bill %q no longer in bills.json: %v", d.Name, err)
				continue
			}
			logging.Infof("bootstrap: removed bill %q (no longer in bills.json)", d.Name)
		}
	}
}

// attachVendorConnection upserts billDefID's bt_vendor_connections row from
// entry's connector fields, then immediately runs a refresh (rather than
// waiting for the scheduler's next tick) so the bill's amount/due-date
// instance are populated right away - same "don't wait for the next tick"
// pattern as syncAccountAsync on the hhq side.
func (a *App) attachVendorConnection(ctx context.Context, sched *scheduler.Scheduler, billDefID int, entry config.BillBootstrap) {
	if entry.Username == "" || entry.Password == "" {
		logging.Errorf("bootstrap: bill %q has a connector but is missing username/password", entry.Name)
		return
	}

	encryptedPassword, err := a.Encryptor.Encrypt(entry.Password)
	if err != nil {
		logging.Errorf("bootstrap: encrypting password for bill %q: %v", entry.Name, err)
		return
	}

	if _, err := a.Vendors.Upsert(ctx, models.VendorConnection{
		BillDefinitionID:  billDefID,
		Connector:         entry.Connector,
		Tenant:            entry.Tenant,
		Username:          entry.Username,
		EncryptedPassword: encryptedPassword,
		BootstrapManaged:  true,
	}); err != nil {
		logging.Errorf("bootstrap: attaching vendor connection to bill %q: %v", entry.Name, err)
		return
	}
	logging.Infof("bootstrap: attached vendor connection %q (%s) to bill %q", entry.Connector, entry.Tenant, entry.Name)

	// Re-fetch rather than building a VendorConnection by hand so
	// LastSyncedAt reflects the row's real history (Upsert doesn't touch
	// it) - RefreshVendorConnectionIfStale needs that to know whether
	// today's bootstrap run can skip re-hitting the vendor.
	conn, err := a.Vendors.GetByBillDefinitionID(ctx, billDefID)
	if err != nil {
		logging.Errorf("bootstrap: re-reading vendor connection for bill %q: %v", entry.Name, err)
		return
	}
	sched.RefreshVendorConnectionIfStale(ctx, *conn)
}

func billBootstrapToDefinition(entry config.BillBootstrap) (models.BillDefinition, error) {
	if entry.Name == "" {
		return models.BillDefinition{}, fmt.Errorf("name is required")
	}

	def := models.BillDefinition{
		Name:        entry.Name,
		AmountCents: int(entry.Amount*100 + 0.5),
		VendorURL:   sql.NullString{String: entry.VendorURL, Valid: entry.VendorURL != ""},
	}

	if entry.Connector != "" {
		def.ScheduleType = models.ScheduleVendor
		return def, nil
	}

	switch entry.Schedule {
	case "monthly":
		if entry.DayOfMonth == nil || *entry.DayOfMonth < 1 || *entry.DayOfMonth > 31 {
			return models.BillDefinition{}, fmt.Errorf("day_of_month must be between 1 and 31 for a monthly bill")
		}
		def.ScheduleType = models.ScheduleMonthly
		def.DayOfMonth = sql.NullInt16{Int16: int16(*entry.DayOfMonth), Valid: true}
	case "quarterly":
		if entry.DayOfMonth == nil || *entry.DayOfMonth < 1 || *entry.DayOfMonth > 31 {
			return models.BillDefinition{}, fmt.Errorf("day_of_month must be between 1 and 31 for a quarterly bill")
		}
		if entry.QuarterStartMonth == nil || *entry.QuarterStartMonth < 1 || *entry.QuarterStartMonth > 3 {
			return models.BillDefinition{}, fmt.Errorf("quarter_start_month must be 1, 2, or 3 for a quarterly bill")
		}
		def.ScheduleType = models.ScheduleQuarterly
		def.DayOfMonth = sql.NullInt16{Int16: int16(*entry.DayOfMonth), Valid: true}
		def.QuarterStartMonth = sql.NullInt16{Int16: int16(*entry.QuarterStartMonth), Valid: true}
	case "one_off":
		due, err := time.Parse("2006-01-02", entry.OneOffDate)
		if err != nil {
			return models.BillDefinition{}, fmt.Errorf("one_off_date must be YYYY-MM-DD: %w", err)
		}
		def.ScheduleType = models.ScheduleOneOff
		def.OneOffDate = sql.NullTime{Time: due, Valid: true}
	default:
		return models.BillDefinition{}, fmt.Errorf("schedule must be 'monthly', 'quarterly', or 'one_off', got %q", entry.Schedule)
	}

	return def, nil
}
