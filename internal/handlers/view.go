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
	"fmt"
	"net/http"
	"time"
)

// formatCents renders integer cents as a "$d.dd" string for display only -
// storage/arithmetic always stays in integer cents (see internal/models).
func formatCents(cents int) string {
	return fmt.Sprintf("$%.2f", float64(cents)/100)
}

// formatCentsPlain is formatCents without the currency symbol, for
// pre-filling a number input's value attribute.
func formatCentsPlain(cents int) string {
	return fmt.Sprintf("%.2f", float64(cents)/100)
}

// viewLookaheadDays bounds how far out an upcoming (not-yet-due) bill is
// still shown on the full-screen view - wider than the old widget's 14-day
// window since a full screen has room for more, and there's no configurable
// column count to fit within anymore.
const viewLookaheadDays = 60

type viewBillRow struct {
	Name    string
	Amount  string
	Due     time.Time
	Overdue bool
}

type viewAccountRow struct {
	Name     string
	OrgName  string
	Balance  string
	IsCredit bool
}

type viewData struct {
	Bills              []viewBillRow
	Accounts           []viewAccountRow
	SimpleFinConnected bool
}

// View handles GET /view - the full-screen HTML page inlined server-side
// into hhq's kiosk content region when a parent/child taps the plugin's nav
// button (see hhq's internal/plugins.FetchView doc comment). Never fetched
// by a browser directly, so all styling must be self-contained inline
// <style>, not a linked stylesheet - same trust/reachability reasoning as
// the removed widget.html had. Unlike the old widget, this always shows
// every available field (name/amount/due date/status/vendor link) - there's
// no column configurability to apply since a full screen has room for all
// of it.
func (a *App) View(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	until := today.AddDate(0, 0, viewLookaheadDays)

	instances, err := a.Instances.ListUpcomingUnpaid(ctx, until)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	bills := make([]viewBillRow, 0, len(instances))
	for _, inst := range instances {
		bills = append(bills, viewBillRow{
			Name:    inst.Name,
			Amount:  formatCents(inst.AmountCents),
			Due:     inst.DueDate,
			Overdue: inst.DueDate.Before(today),
		})
	}

	data := viewData{Bills: bills}

	if _, connErr := a.SimpleFin.Get(ctx); connErr == nil {
		data.SimpleFinConnected = true
		accounts, err := a.Accounts.ListVisible(ctx)
		if err == nil {
			var bankRows, creditRows []viewAccountRow
			for _, acc := range accounts {
				row := viewAccountRow{
					Name:     acc.EffectiveName(),
					OrgName:  acc.OrgName.String,
					Balance:  formatCents(int(acc.BalanceCents)),
					IsCredit: acc.IsCredit(),
				}
				if row.IsCredit {
					creditRows = append(creditRows, row)
				} else {
					bankRows = append(bankRows, row)
				}
			}
			data.Accounts = append(bankRows, creditRows...)
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.Templates.ExecuteTemplate(w, "view", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
