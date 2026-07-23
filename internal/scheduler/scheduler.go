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

// Package scheduler runs background jobs: generating upcoming bill
// instances from their recurring/one-off definitions, refreshing SimpleFIN
// account balances, and refreshing bills that have a vendor connector
// attached. Plain goroutines + time.Ticker, no cron library - mirrors hhq's
// own internal/scheduler approach.
package scheduler

import (
	"context"
	"database/sql"
	"time"

	"github.com/mscreations/billtracker-plugin/internal/config"
	"github.com/mscreations/billtracker-plugin/internal/connectors"
	"github.com/mscreations/billtracker-plugin/internal/logging"
	"github.com/mscreations/billtracker-plugin/internal/models"
	"github.com/mscreations/billtracker-plugin/internal/simplefin"
	"github.com/mscreations/billtracker-plugin/internal/util"
)

const instanceGenerationInterval = 6 * time.Hour

type Scheduler struct {
	Cfg *config.Config

	BillDefs  *models.BillDefinitionStore
	Instances *models.BillInstanceStore
	Accounts  *models.AccountStore
	SimpleFin *models.SimpleFinConnectionStore
	Vendors   *models.VendorConnectionStore
	Encryptor *util.Encryptor
}

// Run launches every background job and blocks until ctx is cancelled.
func (s *Scheduler) Run(ctx context.Context) {
	go s.runInstanceGeneration(ctx)
	go s.runSimpleFinRefresh(ctx)
	go s.runVendorRefresh(ctx)
	<-ctx.Done()
}

func (s *Scheduler) runInstanceGeneration(ctx context.Context) {
	s.generateInstances(ctx)

	ticker := time.NewTicker(instanceGenerationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.generateInstances(ctx)
		}
	}
}

func (s *Scheduler) generateInstances(ctx context.Context) {
	defs, err := s.BillDefs.ListAll(ctx)
	if err != nil {
		logging.Errorf("scheduler: listing bill definitions: %v", err)
		return
	}
	for _, def := range defs {
		if err := GenerateInstancesForDefinition(ctx, s.Instances, def, s.Cfg.BillInstanceLookaheadDays); err != nil {
			logging.Errorf("scheduler: generating instances for %q: %v", def.Name, err)
		}
	}
	logging.Debugf("scheduler: generated bill instances for %d definitions", len(defs))
}

// GenerateInstancesForDefinition ensures every occurrence of def between now
// and now+lookaheadDays has a bt_bill_instances row. Exported so
// handlers.App can call it immediately after a bill is created/edited,
// rather than waiting for the next ticker (same "don't wait for the next
// tick" pattern as hhq's syncAccountAsync).
func GenerateInstancesForDefinition(ctx context.Context, instances *models.BillInstanceStore, def models.BillDefinition, lookaheadDays int) error {
	// Time.Truncate rounds to a boundary since the absolute zero time (UTC),
	// not local midnight - for any non-UTC TZ this can silently land "today"
	// on the wrong local calendar day. Build local midnight explicitly instead.
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	until := today.AddDate(0, 0, lookaheadDays)

	for _, due := range models.NextDueDates(def, today, until) {
		if err := instances.EnsureInstance(ctx, def.ID, due); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) runSimpleFinRefresh(ctx context.Context) {
	s.RefreshSimpleFinNow(ctx)

	interval := time.Duration(s.Cfg.SimpleFinRefreshIntervalMinutes) * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RefreshSimpleFinNow(ctx)
		}
	}
}

// RefreshSimpleFinNow no-ops cleanly if no connection is configured yet.
// Exported so handlers.App can trigger an immediate refresh right after a
// parent connects SimpleFIN or clicks "Refresh now", instead of waiting for
// the next ticker.
func (s *Scheduler) RefreshSimpleFinNow(ctx context.Context) {
	conn, err := s.SimpleFin.Get(ctx)
	if err != nil {
		if err != models.ErrNoSimpleFinConnection {
			logging.Errorf("scheduler: loading SimpleFIN connection: %v", err)
		}
		return
	}
	accessURL, err := s.Encryptor.Decrypt(conn.EncryptedAccessURL)
	if err != nil {
		logging.Errorf("scheduler: decrypting SimpleFIN access URL: %v", err)
		_ = s.SimpleFin.MarkSynced(ctx, conn.ID, err)
		return
	}

	accounts, err := simplefin.FetchAccounts(ctx, accessURL)
	if err != nil {
		logging.Errorf("scheduler: fetching SimpleFIN accounts: %v", err)
		_ = s.SimpleFin.MarkSynced(ctx, conn.ID, err)
		return
	}

	for _, acc := range accounts {
		if err := s.Accounts.Upsert(ctx, models.Account{
			SimpleFinID:           acc.ID,
			OrgName:               nullableString(acc.OrgName),
			Name:                  acc.Name,
			Currency:              acc.Currency,
			BalanceCents:          acc.BalanceCents,
			AvailableBalanceCents: nullableInt64Ptr(acc.AvailableBalanceCents),
			BalanceDate:           nullableTimePtr(acc.BalanceDate),
		}); err != nil {
			logging.Errorf("scheduler: upserting account %s: %v", acc.ID, err)
		}
	}

	_ = s.SimpleFin.MarkSynced(ctx, conn.ID, nil)
	logging.Debugf("scheduler: refreshed %d SimpleFIN accounts", len(accounts))
}

func (s *Scheduler) runVendorRefresh(ctx context.Context) {
	s.refreshVendorConnections(ctx)

	interval := time.Duration(s.Cfg.VendorRefreshIntervalMinutes) * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshVendorConnections(ctx)
		}
	}
}

func (s *Scheduler) refreshVendorConnections(ctx context.Context) {
	conns, err := s.Vendors.ListAll(ctx)
	if err != nil {
		logging.Errorf("scheduler: listing vendor connections: %v", err)
		return
	}
	for _, conn := range conns {
		s.RefreshVendorConnectionIfStale(ctx, conn)
	}
}

// RefreshVendorConnectionIfStale is the cached read path: it only actually
// logs into the vendor if conn hasn't been synced within
// VendorRefreshIntervalMinutes, otherwise the already-stored bill amount/
// due date (last written by a prior RefreshVendorConnectionNow) is left as
// is. This is what keeps both the scheduler's own startup call and
// handlers.App's per-bootstrap "sync immediately" call from hitting the
// vendor's site on every single process restart - a bill's amount/due date
// doesn't change between an app restart five minutes after the last one,
// and vendor bill-pay portals are slow and sometimes rate-limit logins.
// A connection with no prior sync (LastSyncedAt unset - brand new) is
// always treated as stale, so a newly attached connection still gets its
// first fetch immediately rather than waiting for the next ticker.
func (s *Scheduler) RefreshVendorConnectionIfStale(ctx context.Context, conn models.VendorConnection) {
	if conn.LastSyncedAt.Valid {
		interval := time.Duration(s.Cfg.VendorRefreshIntervalMinutes) * time.Minute
		if age := time.Since(conn.LastSyncedAt.Time); age < interval {
			logging.Debugf("scheduler: vendor connection %d synced %s ago (within %s) - using cached bill data", conn.ID, age.Round(time.Second), interval)
			return
		}
	}
	s.RefreshVendorConnectionNow(ctx, conn)
}

// RefreshVendorConnectionNow unconditionally logs into conn's vendor portal
// and updates the linked bill's amount and due-date instance - use
// RefreshVendorConnectionIfStale instead unless a forced, uncached refresh
// is specifically what's wanted (e.g. a future "resync now" button).
func (s *Scheduler) RefreshVendorConnectionNow(ctx context.Context, conn models.VendorConnection) {
	connector, err := connectors.Get(conn.Connector)
	if err != nil {
		logging.Errorf("scheduler: vendor connection %d: %v", conn.ID, err)
		_ = s.Vendors.MarkSynced(ctx, conn.ID, "", err)
		return
	}

	password, err := s.Encryptor.Decrypt(conn.EncryptedPassword)
	if err != nil {
		logging.Errorf("scheduler: decrypting password for vendor connection %d: %v", conn.ID, err)
		_ = s.Vendors.MarkSynced(ctx, conn.ID, "", err)
		return
	}

	snapshot, err := connector.FetchBill(ctx, connectors.Credentials{
		Tenant:   conn.Tenant,
		Username: conn.Username,
		Password: password,
	})
	if err != nil {
		logging.Errorf("scheduler: fetching bill for vendor connection %d: %v", conn.ID, err)
		_ = s.Vendors.MarkSynced(ctx, conn.ID, "", err)
		return
	}

	if err := s.BillDefs.UpdateAmount(ctx, conn.BillDefinitionID, int(snapshot.AmountCents)); err != nil {
		logging.Errorf("scheduler: updating amount for bill %d from vendor connection %d: %v", conn.BillDefinitionID, conn.ID, err)
	}
	if snapshot.AmountCents <= 0 {
		logging.Debugf("scheduler: vendor connection %d (bill %d) reports nothing owed (amount=%d) - skipping instance creation", conn.ID, conn.BillDefinitionID, snapshot.AmountCents)
	} else if err := s.Instances.EnsureInstance(ctx, conn.BillDefinitionID, snapshot.DueDate); err != nil {
		logging.Errorf("scheduler: ensuring instance for bill %d from vendor connection %d: %v", conn.BillDefinitionID, conn.ID, err)
	}

	_ = s.Vendors.MarkSynced(ctx, conn.ID, snapshot.AccountNumber, nil)
	logging.Debugf("scheduler: refreshed vendor connection %d (bill %d): amount=%d due=%s", conn.ID, conn.BillDefinitionID, snapshot.AmountCents, snapshot.DueDate.Format("2006-01-02"))
}

func nullableString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullableInt64Ptr(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func nullableTimePtr(v *time.Time) sql.NullTime {
	if v == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *v, Valid: true}
}
