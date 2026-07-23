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

package models

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/mscreations/billtracker-plugin/internal/testutil"
)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

// ---- NextDueDates (pure function) ----

func TestNextDueDatesVendorAlwaysNil(t *testing.T) {
	def := BillDefinition{ScheduleType: ScheduleVendor}
	got := NextDueDates(def, date(2026, 1, 1), date(2026, 12, 31))
	if got != nil {
		t.Fatalf("NextDueDates for vendor schedule = %v, want nil", got)
	}
}

func TestNextDueDatesOneOff(t *testing.T) {
	cases := []struct {
		name string
		def  BillDefinition
		from time.Time
		to   time.Time
		want []time.Time
	}{
		{
			name: "no OneOffDate set",
			def:  BillDefinition{ScheduleType: ScheduleOneOff},
			from: date(2026, 1, 1), to: date(2026, 12, 31),
			want: nil,
		},
		{
			name: "in window",
			def:  BillDefinition{ScheduleType: ScheduleOneOff, OneOffDate: sql.NullTime{Time: date(2026, 3, 15), Valid: true}},
			from: date(2026, 1, 1), to: date(2026, 12, 31),
			want: []time.Time{date(2026, 3, 15)},
		},
		{
			name: "before window",
			def:  BillDefinition{ScheduleType: ScheduleOneOff, OneOffDate: sql.NullTime{Time: date(2025, 12, 31), Valid: true}},
			from: date(2026, 1, 1), to: date(2026, 12, 31),
			want: nil,
		},
		{
			name: "after window",
			def:  BillDefinition{ScheduleType: ScheduleOneOff, OneOffDate: sql.NullTime{Time: date(2027, 1, 1), Valid: true}},
			from: date(2026, 1, 1), to: date(2026, 12, 31),
			want: nil,
		},
		{
			name: "exactly on from boundary",
			def:  BillDefinition{ScheduleType: ScheduleOneOff, OneOffDate: sql.NullTime{Time: date(2026, 1, 1), Valid: true}},
			from: date(2026, 1, 1), to: date(2026, 12, 31),
			want: []time.Time{date(2026, 1, 1)},
		},
		{
			name: "exactly on to boundary",
			def:  BillDefinition{ScheduleType: ScheduleOneOff, OneOffDate: sql.NullTime{Time: date(2026, 12, 31), Valid: true}},
			from: date(2026, 1, 1), to: date(2026, 12, 31),
			want: []time.Time{date(2026, 12, 31)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NextDueDates(tc.def, tc.from, tc.to)
			if len(got) != len(tc.want) {
				t.Fatalf("NextDueDates() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if !got[i].Equal(tc.want[i]) {
					t.Fatalf("NextDueDates()[%d] = %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestNextDueDatesMonthly(t *testing.T) {
	def := BillDefinition{ScheduleType: ScheduleMonthly, DayOfMonth: sql.NullInt16{Int16: 15, Valid: true}}
	got := NextDueDates(def, date(2026, 1, 1), date(2026, 3, 31))
	want := []time.Time{date(2026, 1, 15), date(2026, 2, 15), date(2026, 3, 15)}
	if len(got) != len(want) {
		t.Fatalf("NextDueDates() = %v, want %v", got, want)
	}
	for i := range got {
		if !got[i].Equal(want[i]) {
			t.Fatalf("NextDueDates()[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestNextDueDatesMonthlyMissingDayOfMonth(t *testing.T) {
	def := BillDefinition{ScheduleType: ScheduleMonthly}
	got := NextDueDates(def, date(2026, 1, 1), date(2026, 3, 31))
	if got != nil {
		t.Fatalf("NextDueDates() = %v, want nil when DayOfMonth unset", got)
	}
}

func TestNextDueDatesMonthlyClampsToShortMonth(t *testing.T) {
	def := BillDefinition{ScheduleType: ScheduleMonthly, DayOfMonth: sql.NullInt16{Int16: 31, Valid: true}}
	got := NextDueDates(def, date(2026, 2, 1), date(2026, 2, 28))
	if len(got) != 1 || !got[0].Equal(date(2026, 2, 28)) {
		t.Fatalf("NextDueDates() = %v, want Feb 28 (clamped from 31)", got)
	}
}

func TestNextDueDatesQuarterlyMissingQuarterStartMonth(t *testing.T) {
	def := BillDefinition{ScheduleType: ScheduleQuarterly, DayOfMonth: sql.NullInt16{Int16: 1, Valid: true}}
	got := NextDueDates(def, date(2026, 1, 1), date(2026, 12, 31))
	if got != nil {
		t.Fatalf("NextDueDates() = %v, want nil when QuarterStartMonth unset", got)
	}
}

func TestNextDueDatesQuarterlyRotation(t *testing.T) {
	// QuarterStartMonth=2 -> Feb/May/Aug/Nov.
	def := BillDefinition{
		ScheduleType:      ScheduleQuarterly,
		DayOfMonth:        sql.NullInt16{Int16: 10, Valid: true},
		QuarterStartMonth: sql.NullInt16{Int16: 2, Valid: true},
	}
	got := NextDueDates(def, date(2026, 1, 1), date(2026, 12, 31))
	want := []time.Time{date(2026, 2, 10), date(2026, 5, 10), date(2026, 8, 10), date(2026, 11, 10)}
	if len(got) != len(want) {
		t.Fatalf("NextDueDates() = %v, want %v", got, want)
	}
	for i := range got {
		if !got[i].Equal(want[i]) {
			t.Fatalf("NextDueDates()[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestNextDueDatesEmptyWindowReturnsNoOccurrences(t *testing.T) {
	def := BillDefinition{ScheduleType: ScheduleMonthly, DayOfMonth: sql.NullInt16{Int16: 5, Valid: true}}
	// A window entirely within a single month, after the 5th - the cursor
	// starts at the 1st of that month, computes occurrence=5th, but it's
	// before `from`, so it's skipped without ever being appended.
	got := NextDueDates(def, date(2026, 6, 10), date(2026, 6, 20))
	if got != nil {
		t.Fatalf("NextDueDates() = %v, want nil", got)
	}
}

// ---- BillDefinitionStore ----

func TestBillDefinitionStoreCreateGetUpdateDelete(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &BillDefinitionStore{DB: conn}
	ctx := t.Context()

	id, err := s.Create(ctx, BillDefinition{
		Name:         "Electric",
		AmountCents:  15000,
		ScheduleType: ScheduleMonthly,
		DayOfMonth:   sql.NullInt16{Int16: 20, Valid: true},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == 0 {
		t.Fatal("expected a non-zero id")
	}

	got, err := s.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Electric" || got.AmountCents != 15000 {
		t.Fatalf("unexpected definition: %+v", got)
	}

	byName, err := s.GetByName(ctx, "Electric")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if byName.ID != id {
		t.Fatalf("GetByName id = %d, want %d", byName.ID, id)
	}

	all, err := s.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ListAll = %d rows, want 1", len(all))
	}

	if err := s.Update(ctx, id, BillDefinition{
		Name:         "Electric Renamed",
		AmountCents:  16000,
		ScheduleType: ScheduleMonthly,
		DayOfMonth:   sql.NullInt16{Int16: 21, Valid: true},
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = s.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got.Name != "Electric Renamed" || got.AmountCents != 16000 {
		t.Fatalf("unexpected definition after update: %+v", got)
	}

	if err := s.UpdateAmount(ctx, id, 17777); err != nil {
		t.Fatalf("UpdateAmount: %v", err)
	}
	got, _ = s.GetByID(ctx, id)
	if got.AmountCents != 17777 {
		t.Fatalf("AmountCents after UpdateAmount = %d, want 17777", got.AmountCents)
	}

	if err := s.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.GetByID(ctx, id); !errors.Is(err, ErrBillNotFound) {
		t.Fatalf("GetByID after delete: err = %v, want ErrBillNotFound", err)
	}
}

func TestBillDefinitionStoreUpdateBootstrap(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &BillDefinitionStore{DB: conn}
	ctx := t.Context()

	id, err := s.Create(ctx, BillDefinition{
		Name:             "Water",
		AmountCents:      5000,
		ScheduleType:     ScheduleOneOff,
		OneOffDate:       sql.NullTime{Time: date(2026, 4, 1), Valid: true},
		BootstrapManaged: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = s.UpdateBootstrap(ctx, id, BillDefinition{
		AmountCents:  6000,
		ScheduleType: ScheduleOneOff,
		OneOffDate:   sql.NullTime{Time: date(2026, 5, 1), Valid: true},
	})
	if err != nil {
		t.Fatalf("UpdateBootstrap: %v", err)
	}

	got, err := s.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	// UpdateBootstrap deliberately does not touch Name.
	if got.Name != "Water" {
		t.Fatalf("Name = %q, want unchanged 'Water'", got.Name)
	}
	if got.AmountCents != 6000 {
		t.Fatalf("AmountCents = %d, want 6000", got.AmountCents)
	}
	// A DATE column round-trips through pgx tagged UTC regardless of how it
	// was written (see CLAUDE.md Round 17) - compare the calendar day only,
	// not the exact instant, to avoid a spurious mismatch based on the
	// test-runner's local timezone offset.
	y, m, d := got.OneOffDate.Time.Date()
	if y != 2026 || m != 5 || d != 1 {
		t.Fatalf("OneOffDate = %v, want calendar day 2026-05-01", got.OneOffDate.Time)
	}
}

func TestBillDefinitionStoreGetByIDNotFound(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &BillDefinitionStore{DB: conn}
	if _, err := s.GetByID(t.Context(), 999999); !errors.Is(err, ErrBillNotFound) {
		t.Fatalf("GetByID: err = %v, want ErrBillNotFound", err)
	}
}

func TestBillDefinitionStoreGetByNameNotFound(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &BillDefinitionStore{DB: conn}
	if _, err := s.GetByName(t.Context(), "does-not-exist"); !errors.Is(err, ErrBillNotFound) {
		t.Fatalf("GetByName: err = %v, want ErrBillNotFound", err)
	}
}

func TestBillDefinitionStoreQueriesReturnErrorOnCanceledContext(t *testing.T) {
	conn := testutil.RequireDB(t)
	s := &BillDefinitionStore{DB: conn}

	id, err := s.Create(t.Context(), BillDefinition{
		Name: "cancel-ctx", AmountCents: 100, ScheduleType: ScheduleMonthly,
		DayOfMonth: sql.NullInt16{Int16: 1, Valid: true},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := s.ListAll(ctx); err == nil {
		t.Error("ListAll: expected error on canceled context")
	}
	if _, err := s.GetByID(ctx, id); err == nil {
		t.Error("GetByID: expected error on canceled context")
	}
	if _, err := s.GetByName(ctx, "cancel-ctx"); err == nil {
		t.Error("GetByName: expected error on canceled context")
	}
	if _, err := s.Create(ctx, BillDefinition{Name: "x", ScheduleType: ScheduleMonthly, DayOfMonth: sql.NullInt16{Int16: 1, Valid: true}}); err == nil {
		t.Error("Create: expected error on canceled context")
	}
	if err := s.Update(ctx, id, BillDefinition{Name: "x", ScheduleType: ScheduleMonthly, DayOfMonth: sql.NullInt16{Int16: 1, Valid: true}}); err == nil {
		t.Error("Update: expected error on canceled context")
	}
	if err := s.UpdateBootstrap(ctx, id, BillDefinition{ScheduleType: ScheduleMonthly, DayOfMonth: sql.NullInt16{Int16: 1, Valid: true}}); err == nil {
		t.Error("UpdateBootstrap: expected error on canceled context")
	}
	if err := s.UpdateAmount(ctx, id, 1); err == nil {
		t.Error("UpdateAmount: expected error on canceled context")
	}
	if err := s.Delete(ctx, id); err == nil {
		t.Error("Delete: expected error on canceled context")
	}
}

// ---- BillInstanceStore ----

func TestBillInstanceStoreEnsureInstanceIsIdempotent(t *testing.T) {
	conn := testutil.RequireDB(t)
	defs := &BillDefinitionStore{DB: conn}
	insts := &BillInstanceStore{DB: conn}
	ctx := t.Context()

	defID, err := defs.Create(ctx, BillDefinition{
		Name: "Gas", AmountCents: 4000, ScheduleType: ScheduleMonthly,
		DayOfMonth: sql.NullInt16{Int16: 5, Valid: true},
	})
	if err != nil {
		t.Fatalf("Create def: %v", err)
	}

	due := date(2026, 7, 5)
	if err := insts.EnsureInstance(ctx, defID, due); err != nil {
		t.Fatalf("EnsureInstance (1st): %v", err)
	}
	if err := insts.EnsureInstance(ctx, defID, due); err != nil {
		t.Fatalf("EnsureInstance (2nd, same due date): %v", err)
	}

	all, err := insts.ListAllForSettings(ctx, date(2026, 1, 1), date(2026, 12, 31))
	if err != nil {
		t.Fatalf("ListAllForSettings: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ListAllForSettings = %d rows, want exactly 1 (idempotent EnsureInstance)", len(all))
	}
	if all[0].Name != "Gas" || all[0].AmountCents != 4000 || all[0].Paid {
		t.Fatalf("unexpected instance view: %+v", all[0])
	}
}

func TestBillInstanceStoreMarkPaid(t *testing.T) {
	conn := testutil.RequireDB(t)
	defs := &BillDefinitionStore{DB: conn}
	insts := &BillInstanceStore{DB: conn}
	ctx := t.Context()

	defID, err := defs.Create(ctx, BillDefinition{
		Name: "Internet", AmountCents: 8000, ScheduleType: ScheduleMonthly,
		DayOfMonth: sql.NullInt16{Int16: 10, Valid: true},
	})
	if err != nil {
		t.Fatalf("Create def: %v", err)
	}
	due := date(2026, 7, 10)
	if err := insts.EnsureInstance(ctx, defID, due); err != nil {
		t.Fatalf("EnsureInstance: %v", err)
	}

	all, err := insts.ListAllForSettings(ctx, date(2026, 1, 1), date(2026, 12, 31))
	if err != nil || len(all) != 1 {
		t.Fatalf("ListAllForSettings: %v (%d rows)", err, len(all))
	}
	instID := all[0].InstanceID

	unpaid, err := insts.ListUpcomingUnpaid(ctx, date(2026, 12, 31))
	if err != nil {
		t.Fatalf("ListUpcomingUnpaid: %v", err)
	}
	if len(unpaid) != 1 {
		t.Fatalf("ListUpcomingUnpaid = %d rows, want 1", len(unpaid))
	}

	if err := insts.MarkPaid(ctx, instID); err != nil {
		t.Fatalf("MarkPaid: %v", err)
	}

	unpaid, err = insts.ListUpcomingUnpaid(ctx, date(2026, 12, 31))
	if err != nil {
		t.Fatalf("ListUpcomingUnpaid after MarkPaid: %v", err)
	}
	if len(unpaid) != 0 {
		t.Fatalf("ListUpcomingUnpaid after MarkPaid = %d rows, want 0", len(unpaid))
	}

	all, err = insts.ListAllForSettings(ctx, date(2026, 1, 1), date(2026, 12, 31))
	if err != nil || len(all) != 1 || !all[0].Paid {
		t.Fatalf("ListAllForSettings after MarkPaid: %v %+v", err, all)
	}
}

func TestBillInstanceStoreMarkPaidNotFound(t *testing.T) {
	conn := testutil.RequireDB(t)
	insts := &BillInstanceStore{DB: conn}
	err := insts.MarkPaid(t.Context(), 999999)
	if err == nil {
		t.Fatal("expected an error for a nonexistent instance id")
	}
}

func TestBillInstanceStoreListUpcomingUnpaidHasNoLowerBound(t *testing.T) {
	conn := testutil.RequireDB(t)
	defs := &BillDefinitionStore{DB: conn}
	insts := &BillInstanceStore{DB: conn}
	ctx := t.Context()

	defID, err := defs.Create(ctx, BillDefinition{
		Name: "Old Overdue Bill", AmountCents: 100, ScheduleType: ScheduleOneOff,
		OneOffDate: sql.NullTime{Time: date(2020, 1, 1), Valid: true},
	})
	if err != nil {
		t.Fatalf("Create def: %v", err)
	}
	if err := insts.EnsureInstance(ctx, defID, date(2020, 1, 1)); err != nil {
		t.Fatalf("EnsureInstance: %v", err)
	}

	unpaid, err := insts.ListUpcomingUnpaid(ctx, date(2026, 12, 31))
	if err != nil {
		t.Fatalf("ListUpcomingUnpaid: %v", err)
	}
	if len(unpaid) != 1 {
		t.Fatalf("ListUpcomingUnpaid = %d rows, want the old overdue bill still included", len(unpaid))
	}
}

func TestBillInstanceStoreQueriesReturnErrorOnCanceledContext(t *testing.T) {
	conn := testutil.RequireDB(t)
	defs := &BillDefinitionStore{DB: conn}
	insts := &BillInstanceStore{DB: conn}

	defID, err := defs.Create(t.Context(), BillDefinition{
		Name: "cancel-ctx-inst", AmountCents: 100, ScheduleType: ScheduleMonthly,
		DayOfMonth: sql.NullInt16{Int16: 1, Valid: true},
	})
	if err != nil {
		t.Fatalf("Create def: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := insts.ListUpcomingUnpaid(ctx, date(2026, 12, 31)); err == nil {
		t.Error("ListUpcomingUnpaid: expected error on canceled context")
	}
	if _, err := insts.ListAllForSettings(ctx, date(2026, 1, 1), date(2026, 12, 31)); err == nil {
		t.Error("ListAllForSettings: expected error on canceled context")
	}
	if err := insts.EnsureInstance(ctx, defID, date(2026, 1, 1)); err == nil {
		t.Error("EnsureInstance: expected error on canceled context")
	}
	if err := insts.MarkPaid(ctx, 1); err == nil {
		t.Error("MarkPaid: expected error on canceled context")
	}
}
