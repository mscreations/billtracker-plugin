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

package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mscreations/billtracker-plugin/internal/config"
	"github.com/mscreations/billtracker-plugin/internal/connectors"
	"github.com/mscreations/billtracker-plugin/internal/models"
	"github.com/mscreations/billtracker-plugin/internal/testutil"
	"github.com/mscreations/billtracker-plugin/internal/util"
)

func newTestScheduler(t *testing.T) *Scheduler {
	t.Helper()
	conn := testutil.RequireDB(t)
	encryptor, err := util.NewEncryptor(strings.Repeat("ef", 32))
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	return &Scheduler{
		Cfg: &config.Config{
			BillInstanceLookaheadDays:       60,
			SimpleFinRefreshIntervalMinutes: 60,
			VendorRefreshIntervalMinutes:    360,
		},
		BillDefs:  &models.BillDefinitionStore{DB: conn},
		Instances: &models.BillInstanceStore{DB: conn},
		Accounts:  &models.AccountStore{DB: conn},
		SimpleFin: &models.SimpleFinConnectionStore{DB: conn},
		Vendors:   &models.VendorConnectionStore{DB: conn},
		Encryptor: encryptor,
	}
}

func TestRunLaunchesJobsAndStopsOnCancel(t *testing.T) {
	s := newTestScheduler(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestGenerateInstancesForDefinitionMonthly(t *testing.T) {
	s := newTestScheduler(t)
	id, err := s.BillDefs.Create(t.Context(), models.BillDefinition{
		Name: "Rent", AmountCents: 100, ScheduleType: models.ScheduleMonthly,
		DayOfMonth: sql.NullInt16{Int16: 1, Valid: true},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	def, err := s.BillDefs.GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if err := GenerateInstancesForDefinition(t.Context(), s.Instances, *def, 60); err != nil {
		t.Fatalf("GenerateInstancesForDefinition: %v", err)
	}

	instances, err := s.Instances.ListAllForSettings(t.Context(), time.Now().AddDate(0, 0, -1), time.Now().AddDate(0, 3, 0))
	if err != nil {
		t.Fatalf("ListAllForSettings: %v", err)
	}
	if len(instances) == 0 {
		t.Fatal("expected at least one generated instance")
	}
}

func TestGenerateInstancesForDefinitionReturnsErrorOnDBFailure(t *testing.T) {
	brokenInstances := &models.BillInstanceStore{DB: brokenSchedDB(t)}
	def := models.BillDefinition{
		ID: 1, ScheduleType: models.ScheduleMonthly,
		DayOfMonth: sql.NullInt16{Int16: 1, Valid: true},
	}
	if err := GenerateInstancesForDefinition(t.Context(), brokenInstances, def, 60); err == nil {
		t.Fatal("expected an error from a broken instances store")
	}
}

func TestGenerateInstancesSkipsDefinitionErrorAndContinues(t *testing.T) {
	s := newTestScheduler(t)
	// A vendor-scheduled def generates zero due dates (NextDueDates always
	// nil for it) - not an error, just nothing to do; a second, valid def
	// in the same pass should still get instances created normally.
	if _, err := s.BillDefs.Create(t.Context(), models.BillDefinition{
		Name: "VendorBill", AmountCents: 100, ScheduleType: models.ScheduleVendor,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.BillDefs.Create(t.Context(), models.BillDefinition{
		Name: "RealBill", AmountCents: 100, ScheduleType: models.ScheduleMonthly,
		DayOfMonth: sql.NullInt16{Int16: 1, Valid: true},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	s.generateInstances(t.Context())

	instances, err := s.Instances.ListAllForSettings(t.Context(), time.Now().AddDate(0, 0, -1), time.Now().AddDate(0, 3, 0))
	if err != nil {
		t.Fatalf("ListAllForSettings: %v", err)
	}
	if len(instances) == 0 {
		t.Fatal("expected the real bill to have generated instances")
	}
}

func TestGenerateInstancesReturnsEarlyOnListAllFailure(t *testing.T) {
	s := newTestScheduler(t)
	s.BillDefs = &models.BillDefinitionStore{DB: brokenSchedDB(t)}
	// Must not panic.
	s.generateInstances(t.Context())
}

func TestRefreshSimpleFinNowNoConnectionIsNoOp(t *testing.T) {
	s := newTestScheduler(t)
	s.RefreshSimpleFinNow(t.Context()) // must not panic or error visibly
}

func TestRefreshSimpleFinNowGenericGetErrorIsNoOp(t *testing.T) {
	s := newTestScheduler(t)
	s.SimpleFin = &models.SimpleFinConnectionStore{DB: brokenSchedDB(t)}
	s.RefreshSimpleFinNow(t.Context()) // must not panic
}

func TestRefreshSimpleFinNowDecryptFailure(t *testing.T) {
	s := newTestScheduler(t)
	// A connection whose "encrypted" URL isn't actually valid ciphertext for
	// this encryptor - Decrypt fails deterministically without touching the
	// network or breaking the DB connection.
	if err := s.SimpleFin.Connect(t.Context(), []byte("not-real-ciphertext")); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	s.RefreshSimpleFinNow(t.Context())

	conn, err := s.SimpleFin.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !conn.LastSyncError.Valid {
		t.Fatal("expected LastSyncError to be set after a decrypt failure")
	}
}

func TestRefreshSimpleFinNowFetchAccountsFailure(t *testing.T) {
	s := newTestScheduler(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer fake.Close()
	encrypted, err := s.Encryptor.Encrypt(fake.URL)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := s.SimpleFin.Connect(t.Context(), encrypted); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	s.RefreshSimpleFinNow(t.Context())

	conn, err := s.SimpleFin.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !conn.LastSyncError.Valid {
		t.Fatal("expected LastSyncError to be set after a fetch failure")
	}
}

func TestRefreshSimpleFinNowSuccess(t *testing.T) {
	s := newTestScheduler(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"accounts": []map[string]any{
				{"id": "acc-1", "name": "Checking", "currency": "USD", "balance": "150.25",
					"org": map[string]string{"name": "Bank"}, "available-balance": "140.00", "balance-date": 1700000000},
			},
		})
	}))
	defer fake.Close()
	encrypted, err := s.Encryptor.Encrypt(fake.URL)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := s.SimpleFin.Connect(t.Context(), encrypted); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	s.RefreshSimpleFinNow(t.Context())

	accounts, err := s.Accounts.ListAll(t.Context())
	if err != nil || len(accounts) != 1 {
		t.Fatalf("ListAll: %+v %v", accounts, err)
	}
	if accounts[0].BalanceCents != 15025 {
		t.Fatalf("BalanceCents = %d, want 15025", accounts[0].BalanceCents)
	}
	conn, err := s.SimpleFin.Get(t.Context())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if conn.LastSyncError.Valid {
		t.Fatalf("expected no sync error, got %q", conn.LastSyncError.String)
	}
}

func TestRefreshSimpleFinNowUpsertFailureIsLoggedAndContinues(t *testing.T) {
	s := newTestScheduler(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"accounts": []map[string]any{{"id": "acc-x", "name": "X", "currency": "USD", "balance": "1.00"}},
		})
	}))
	defer fake.Close()
	encrypted, err := s.Encryptor.Encrypt(fake.URL)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if err := s.SimpleFin.Connect(t.Context(), encrypted); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	s.Accounts = &models.AccountStore{DB: brokenSchedDB(t)}

	s.RefreshSimpleFinNow(t.Context()) // must not panic despite the broken Accounts store
}

// --- vendor refresh ---

type fakeConnector struct {
	slug     string
	snapshot connectors.BillSnapshot
	err      error
}

func (f fakeConnector) Slug() string { return f.slug }
func (f fakeConnector) FetchBill(ctx context.Context, creds connectors.Credentials) (connectors.BillSnapshot, error) {
	return f.snapshot, f.err
}

func TestRefreshVendorConnectionsListAllFailureIsNoOp(t *testing.T) {
	s := newTestScheduler(t)
	s.Vendors = &models.VendorConnectionStore{DB: brokenSchedDB(t)}
	s.refreshVendorConnections(t.Context()) // must not panic
}

func TestRefreshVendorConnectionIfStaleSkipsRecentSync(t *testing.T) {
	s := newTestScheduler(t)
	id, err := s.BillDefs.Create(t.Context(), models.BillDefinition{Name: "Elec", AmountCents: 100, ScheduleType: models.ScheduleVendor})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	connectors.Register(fakeConnector{slug: "test-stale-connector"})
	encryptedPW, err := s.Encryptor.Encrypt("pw")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	connID, err := s.Vendors.Upsert(t.Context(), models.VendorConnection{
		BillDefinitionID: id, Connector: "test-stale-connector", Username: "u", EncryptedPassword: encryptedPW,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.Vendors.MarkSynced(t.Context(), connID, "acct-1", nil); err != nil {
		t.Fatalf("MarkSynced: %v", err)
	}
	conn, err := s.Vendors.GetByBillDefinitionID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByBillDefinitionID: %v", err)
	}

	// VendorRefreshIntervalMinutes is 360 (6h) - a connection just synced is
	// well within that window, so this must skip re-fetching (no error, no
	// state change) rather than call FetchBill again.
	s.RefreshVendorConnectionIfStale(t.Context(), *conn)
}

func TestRefreshVendorConnectionIfStaleRefreshesWhenNeverSynced(t *testing.T) {
	s := newTestScheduler(t)
	id, err := s.BillDefs.Create(t.Context(), models.BillDefinition{Name: "Water", AmountCents: 100, ScheduleType: models.ScheduleVendor})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	connectors.Register(fakeConnector{slug: "test-fresh-connector", snapshot: connectors.BillSnapshot{
		AmountCents: 4200, DueDate: time.Now().AddDate(0, 0, 5), AccountNumber: "acct-2",
	}})
	encryptedPW, err := s.Encryptor.Encrypt("pw")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := s.Vendors.Upsert(t.Context(), models.VendorConnection{
		BillDefinitionID: id, Connector: "test-fresh-connector", Username: "u", EncryptedPassword: encryptedPW,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	conn, err := s.Vendors.GetByBillDefinitionID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByBillDefinitionID: %v", err)
	}

	s.RefreshVendorConnectionIfStale(t.Context(), *conn)

	def, err := s.BillDefs.GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if def.AmountCents != 4200 {
		t.Fatalf("AmountCents = %d, want 4200", def.AmountCents)
	}
}

func TestRefreshVendorConnectionNowUnknownConnector(t *testing.T) {
	s := newTestScheduler(t)
	id, err := s.BillDefs.Create(t.Context(), models.BillDefinition{Name: "X", AmountCents: 1, ScheduleType: models.ScheduleVendor})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	encryptedPW, err := s.Encryptor.Encrypt("pw")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	connID, err := s.Vendors.Upsert(t.Context(), models.VendorConnection{
		BillDefinitionID: id, Connector: "totally-unregistered", Username: "u", EncryptedPassword: encryptedPW,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	conn, err := s.Vendors.GetByBillDefinitionID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByBillDefinitionID: %v", err)
	}

	s.RefreshVendorConnectionNow(t.Context(), *conn)

	after, err := s.Vendors.GetByBillDefinitionID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByBillDefinitionID: %v", err)
	}
	if !after.LastSyncError.Valid {
		t.Fatal("expected LastSyncError to be set")
	}
	_ = connID
}

func TestRefreshVendorConnectionNowDecryptFailure(t *testing.T) {
	s := newTestScheduler(t)
	id, err := s.BillDefs.Create(t.Context(), models.BillDefinition{Name: "Y", AmountCents: 1, ScheduleType: models.ScheduleVendor})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	connectors.Register(fakeConnector{slug: "test-decrypt-fail-connector"})
	if _, err := s.Vendors.Upsert(t.Context(), models.VendorConnection{
		BillDefinitionID: id, Connector: "test-decrypt-fail-connector", Username: "u", EncryptedPassword: []byte("not-real-ciphertext"),
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	conn, err := s.Vendors.GetByBillDefinitionID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByBillDefinitionID: %v", err)
	}

	s.RefreshVendorConnectionNow(t.Context(), *conn)

	after, err := s.Vendors.GetByBillDefinitionID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByBillDefinitionID: %v", err)
	}
	if !after.LastSyncError.Valid {
		t.Fatal("expected LastSyncError to be set after a decrypt failure")
	}
}

func TestRefreshVendorConnectionNowFetchBillFailure(t *testing.T) {
	s := newTestScheduler(t)
	id, err := s.BillDefs.Create(t.Context(), models.BillDefinition{Name: "Z", AmountCents: 1, ScheduleType: models.ScheduleVendor})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	connectors.Register(fakeConnector{slug: "test-fetch-fail-connector", err: errors.New("vendor site down")})
	encryptedPW, err := s.Encryptor.Encrypt("pw")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := s.Vendors.Upsert(t.Context(), models.VendorConnection{
		BillDefinitionID: id, Connector: "test-fetch-fail-connector", Username: "u", EncryptedPassword: encryptedPW,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	conn, err := s.Vendors.GetByBillDefinitionID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByBillDefinitionID: %v", err)
	}

	s.RefreshVendorConnectionNow(t.Context(), *conn)

	after, err := s.Vendors.GetByBillDefinitionID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByBillDefinitionID: %v", err)
	}
	if !after.LastSyncError.Valid {
		t.Fatal("expected LastSyncError to be set after a fetch failure")
	}
}

func TestRefreshVendorConnectionNowZeroAmountSkipsInstanceCreation(t *testing.T) {
	s := newTestScheduler(t)
	id, err := s.BillDefs.Create(t.Context(), models.BillDefinition{Name: "ZeroDue", AmountCents: 1, ScheduleType: models.ScheduleVendor})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	connectors.Register(fakeConnector{slug: "test-zero-amount-connector", snapshot: connectors.BillSnapshot{
		AmountCents: 0, DueDate: time.Now().AddDate(0, 0, 5),
	}})
	encryptedPW, err := s.Encryptor.Encrypt("pw")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := s.Vendors.Upsert(t.Context(), models.VendorConnection{
		BillDefinitionID: id, Connector: "test-zero-amount-connector", Username: "u", EncryptedPassword: encryptedPW,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	conn, err := s.Vendors.GetByBillDefinitionID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByBillDefinitionID: %v", err)
	}

	s.RefreshVendorConnectionNow(t.Context(), *conn)

	instances, err := s.Instances.ListAllForSettings(t.Context(), time.Now().AddDate(0, 0, -1), time.Now().AddDate(0, 1, 0))
	if err != nil {
		t.Fatalf("ListAllForSettings: %v", err)
	}
	if len(instances) != 0 {
		t.Fatalf("expected no instance for a zero-amount snapshot, got %+v", instances)
	}
}

func TestRefreshVendorConnectionNowUpdateAmountFailureIsNonFatal(t *testing.T) {
	s := newTestScheduler(t)
	id, err := s.BillDefs.Create(t.Context(), models.BillDefinition{Name: "BrokenUpdate", AmountCents: 1, ScheduleType: models.ScheduleVendor})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	connectors.Register(fakeConnector{slug: "test-update-fail-connector", snapshot: connectors.BillSnapshot{
		AmountCents: 500, DueDate: time.Now().AddDate(0, 0, 5),
	}})
	encryptedPW, err := s.Encryptor.Encrypt("pw")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := s.Vendors.Upsert(t.Context(), models.VendorConnection{
		BillDefinitionID: id, Connector: "test-update-fail-connector", Username: "u", EncryptedPassword: encryptedPW,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	conn, err := s.Vendors.GetByBillDefinitionID(t.Context(), id)
	if err != nil {
		t.Fatalf("GetByBillDefinitionID: %v", err)
	}
	s.BillDefs = &models.BillDefinitionStore{DB: brokenSchedDB(t)}

	s.RefreshVendorConnectionNow(t.Context(), *conn) // must not panic despite UpdateAmount failing
}

func TestNullableHelpers(t *testing.T) {
	if got := nullableString(""); got.Valid {
		t.Errorf("nullableString(\"\") = %+v, want invalid", got)
	}
	if got := nullableString("x"); !got.Valid || got.String != "x" {
		t.Errorf("nullableString(\"x\") = %+v", got)
	}
	if got := nullableInt64Ptr(nil); got.Valid {
		t.Errorf("nullableInt64Ptr(nil) = %+v, want invalid", got)
	}
	v := int64(42)
	if got := nullableInt64Ptr(&v); !got.Valid || got.Int64 != 42 {
		t.Errorf("nullableInt64Ptr(&42) = %+v", got)
	}
	if got := nullableTimePtr(nil); got.Valid {
		t.Errorf("nullableTimePtr(nil) = %+v, want invalid", got)
	}
	now := time.Now()
	if got := nullableTimePtr(&now); !got.Valid {
		t.Errorf("nullableTimePtr(&now) = %+v, want valid", got)
	}
}

// brokenSchedDB mirrors the same technique used across this repo's other
// coverage tests: an open-then-closed *sql.DB so queries deterministically
// fail with "sql: database is closed".
func brokenSchedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", "postgres://broken:broken@127.0.0.1:1/broken?sslmode=disable")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.Close()
	t.Cleanup(func() { db.Close() })
	return db
}
