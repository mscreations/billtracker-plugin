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

// Package models owns all database access for the Bill Tracker plugin. Every
// table here is prefixed bt_ and lives in its own namespace within whatever
// Postgres database this plugin is pointed at - even when that happens to be
// the same database hhq itself uses, this package never reads hhq's own
// tables (not guaranteed to be true in every deployment).
package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrBillNotFound = errors.New("bill definition not found")

type ScheduleType string

const (
	ScheduleMonthly   ScheduleType = "monthly"
	ScheduleQuarterly ScheduleType = "quarterly"
	ScheduleOneOff    ScheduleType = "one_off"
	// ScheduleVendor marks a bill definition whose due date and amount are
	// entirely driven by a vendor connector (see internal/connectors)
	// rather than a hand-configured recurrence. NextDueDates always
	// returns nil for it - the scheduler's vendor-refresh job creates its
	// own instance directly (BillInstanceStore.EnsureInstance) once it
	// knows the vendor's actual current due date, instead of this generic
	// schedule engine guessing at one.
	ScheduleVendor ScheduleType = "vendor"
)

// BillDefinition is the recurring/one-off rule for a bill. Actual due-date
// occurrences and paid status live in BillInstance.
type BillDefinition struct {
	ID                 int
	Name               string
	AmountCents        int
	ScheduleType       ScheduleType
	DayOfMonth         sql.NullInt16 // set iff ScheduleType == monthly or quarterly
	QuarterStartMonth  sql.NullInt16 // set iff ScheduleType == quarterly; 1=Jan/Apr/Jul/Oct, 2=Feb/May/Aug/Nov, 3=Mar/Jun/Sep/Dec
	OneOffDate         sql.NullTime  // set iff ScheduleType == one_off
	VendorURL          sql.NullString
	SimpleFinAccountID sql.NullInt32
	BootstrapManaged   bool
	CreatedAt          time.Time
}

// BillInstance is one due-date occurrence of a BillDefinition.
type BillInstance struct {
	ID               int
	BillDefinitionID int
	DueDate          time.Time
	Paid             bool
	PaidAt           sql.NullTime
}

// BillInstanceView joins an instance with its definition's display fields -
// the shape the view, events, and settings table all actually want.
type BillInstanceView struct {
	InstanceID   int
	DefinitionID int
	Name         string
	AmountCents  int
	VendorURL    sql.NullString
	DueDate      time.Time
	Paid         bool
}

type BillDefinitionStore struct {
	DB *sql.DB
}

func (s *BillDefinitionStore) ListAll(ctx context.Context) ([]BillDefinition, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, name, amount_cents, schedule_type, day_of_month, quarter_start_month, one_off_date,
		       vendor_url, simplefin_account_id, bootstrap_managed, created_at
		FROM bt_bill_definitions
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBillDefinitions(rows)
}

func (s *BillDefinitionStore) GetByID(ctx context.Context, id int) (*BillDefinition, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, name, amount_cents, schedule_type, day_of_month, quarter_start_month, one_off_date,
		       vendor_url, simplefin_account_id, bootstrap_managed, created_at
		FROM bt_bill_definitions WHERE id = $1`, id)
	return scanBillDefinition(row)
}

func (s *BillDefinitionStore) GetByName(ctx context.Context, name string) (*BillDefinition, error) {
	row := s.DB.QueryRowContext(ctx, `
		SELECT id, name, amount_cents, schedule_type, day_of_month, quarter_start_month, one_off_date,
		       vendor_url, simplefin_account_id, bootstrap_managed, created_at
		FROM bt_bill_definitions WHERE name = $1`, name)
	return scanBillDefinition(row)
}

func (s *BillDefinitionStore) Create(ctx context.Context, b BillDefinition) (int, error) {
	var id int
	err := s.DB.QueryRowContext(ctx, `
		INSERT INTO bt_bill_definitions
			(name, amount_cents, schedule_type, day_of_month, quarter_start_month, one_off_date, vendor_url, bootstrap_managed)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		b.Name, b.AmountCents, b.ScheduleType, b.DayOfMonth, b.QuarterStartMonth, b.OneOffDate, b.VendorURL, b.BootstrapManaged).Scan(&id)
	return id, err
}

// Update is the settings-UI-facing edit path - not usable on a
// bootstrap-managed definition (callers must check BootstrapManaged first).
func (s *BillDefinitionStore) Update(ctx context.Context, id int, b BillDefinition) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE bt_bill_definitions
		SET name = $2, amount_cents = $3, schedule_type = $4, day_of_month = $5,
		    quarter_start_month = $6, one_off_date = $7, vendor_url = $8
		WHERE id = $1`,
		id, b.Name, b.AmountCents, b.ScheduleType, b.DayOfMonth, b.QuarterStartMonth, b.OneOffDate, b.VendorURL)
	return err
}

// UpdateBootstrap always overwrites every bootstrap-sourced field to match
// the current bills.json entry - unlike Update, which is the parent-facing
// edit path.
func (s *BillDefinitionStore) UpdateBootstrap(ctx context.Context, id int, b BillDefinition) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE bt_bill_definitions
		SET amount_cents = $2, schedule_type = $3, day_of_month = $4,
		    quarter_start_month = $5, one_off_date = $6, vendor_url = $7
		WHERE id = $1`,
		id, b.AmountCents, b.ScheduleType, b.DayOfMonth, b.QuarterStartMonth, b.OneOffDate, b.VendorURL)
	return err
}

// UpdateAmount is used by a vendor connector refresh (see internal/connectors
// and internal/scheduler) to correct the definition's amount from what the
// vendor's own bill-pay portal currently reports - independent of Update's
// full-edit/bootstrap-managed rules, since a vendor-connected bill's amount
// is meant to track the vendor rather than a hand-entered value.
func (s *BillDefinitionStore) UpdateAmount(ctx context.Context, id int, amountCents int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE bt_bill_definitions SET amount_cents = $2 WHERE id = $1`, id, amountCents)
	return err
}

func (s *BillDefinitionStore) Delete(ctx context.Context, id int) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM bt_bill_definitions WHERE id = $1`, id)
	return err
}

func scanBillDefinitions(rows *sql.Rows) ([]BillDefinition, error) {
	var out []BillDefinition
	for rows.Next() {
		var b BillDefinition
		if err := rows.Scan(&b.ID, &b.Name, &b.AmountCents, &b.ScheduleType, &b.DayOfMonth, &b.QuarterStartMonth,
			&b.OneOffDate, &b.VendorURL, &b.SimpleFinAccountID, &b.BootstrapManaged, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func scanBillDefinition(row *sql.Row) (*BillDefinition, error) {
	var b BillDefinition
	err := row.Scan(&b.ID, &b.Name, &b.AmountCents, &b.ScheduleType, &b.DayOfMonth, &b.QuarterStartMonth,
		&b.OneOffDate, &b.VendorURL, &b.SimpleFinAccountID, &b.BootstrapManaged, &b.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBillNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// NextDueDates returns every occurrence of def between from and to
// (inclusive), used by the scheduler to generate upcoming BillInstance rows.
// A monthly bill with a day_of_month past the end of a short month (e.g. 31
// in February) is clamped to that month's last day.
func NextDueDates(def BillDefinition, from, to time.Time) []time.Time {
	// Truncate(24h) rounds to a boundary since the absolute zero time (UTC),
	// not local midnight in from/to's own Location - for a non-UTC TZ this
	// can silently shift the window by a day. Normalize to each value's own
	// local midnight explicitly instead.
	from = time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	to = time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, to.Location())

	if def.ScheduleType == ScheduleVendor {
		return nil
	}

	if def.ScheduleType == ScheduleOneOff {
		if !def.OneOffDate.Valid {
			return nil
		}
		// A DATE column round-trips through pgx tagged UTC regardless of how
		// it was written - it carries no real timezone, just a calendar day.
		// Reinterpret its Y/M/D in from's Location (Local) rather than
		// comparing instants across two different locations, which would
		// otherwise misclassify a same-day bill as before/after the window.
		od := def.OneOffDate.Time
		d := time.Date(od.Year(), od.Month(), od.Day(), 0, 0, 0, 0, from.Location())
		if d.Before(from) || d.After(to) {
			return nil
		}
		return []time.Time{d}
	}

	if !def.DayOfMonth.Valid {
		return nil
	}
	if def.ScheduleType == ScheduleQuarterly && !def.QuarterStartMonth.Valid {
		return nil
	}
	day := int(def.DayOfMonth.Int16)

	// monthDue reports whether cursor's month is one of the definition's due
	// months - every month for ScheduleMonthly, or one of the 3-month
	// rotation (e.g. Jan/Apr/Jul/Oct) starting at QuarterStartMonth for
	// ScheduleQuarterly.
	monthDue := func(m time.Month) bool {
		if def.ScheduleType != ScheduleQuarterly {
			return true
		}
		start := int(def.QuarterStartMonth.Int16)
		return ((int(m)-start)%3+3)%3 == 0
	}

	var out []time.Time
	cursor := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, from.Location())
	for !cursor.After(to) {
		if monthDue(cursor.Month()) {
			lastDay := cursor.AddDate(0, 1, -1).Day()
			d := day
			if d > lastDay {
				d = lastDay
			}
			occurrence := time.Date(cursor.Year(), cursor.Month(), d, 0, 0, 0, 0, cursor.Location())
			if !occurrence.Before(from) && !occurrence.After(to) {
				out = append(out, occurrence)
			}
		}
		cursor = cursor.AddDate(0, 1, 0)
	}
	return out
}

type BillInstanceStore struct {
	DB *sql.DB
}

// ListUpcomingUnpaid returns every unpaid bill instance due on or before to,
// joined with their definition's display fields, sorted by due date. This is
// what the view and /events endpoint both query. Deliberately has no lower
// bound: an unpaid bill stays relevant (and overdue) indefinitely until it's
// paid, so bounding the query to "from today onward" would silently exclude
// exactly the bills that most need to be shown as late/overdue.
func (s *BillInstanceStore) ListUpcomingUnpaid(ctx context.Context, to time.Time) ([]BillInstanceView, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT i.id, i.bill_definition_id, d.name, d.amount_cents, d.vendor_url, i.due_date, i.paid
		FROM bt_bill_instances i
		JOIN bt_bill_definitions d ON d.id = i.bill_definition_id
		WHERE i.paid = FALSE AND i.due_date <= $1
		ORDER BY i.due_date`, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBillInstanceViews(rows)
}

// ListAllForSettings returns every instance in the given window regardless
// of paid status, for the settings page's bill table.
func (s *BillInstanceStore) ListAllForSettings(ctx context.Context, from, to time.Time) ([]BillInstanceView, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT i.id, i.bill_definition_id, d.name, d.amount_cents, d.vendor_url, i.due_date, i.paid
		FROM bt_bill_instances i
		JOIN bt_bill_definitions d ON d.id = i.bill_definition_id
		WHERE i.due_date BETWEEN $1 AND $2
		ORDER BY i.due_date`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanBillInstanceViews(rows)
}

func scanBillInstanceViews(rows *sql.Rows) ([]BillInstanceView, error) {
	var out []BillInstanceView
	for rows.Next() {
		var v BillInstanceView
		if err := rows.Scan(&v.InstanceID, &v.DefinitionID, &v.Name, &v.AmountCents, &v.VendorURL, &v.DueDate, &v.Paid); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// EnsureInstance creates the instance for (definitionID, dueDate) if it
// doesn't already exist - safe to call repeatedly (e.g. every scheduler
// tick) since (bill_definition_id, due_date) is unique.
func (s *BillInstanceStore) EnsureInstance(ctx context.Context, definitionID int, dueDate time.Time) error {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO bt_bill_instances (bill_definition_id, due_date)
		VALUES ($1, $2)
		ON CONFLICT (bill_definition_id, due_date) DO NOTHING`,
		definitionID, dueDate)
	return err
}

// MarkPaid sets an instance paid - only meaningful on the current
// unpaid/pending occurrence; the next cycle's instance starts fresh.
func (s *BillInstanceStore) MarkPaid(ctx context.Context, id int) error {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE bt_bill_instances SET paid = TRUE, paid_at = now() WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("bill instance %d not found", id)
	}
	return nil
}
