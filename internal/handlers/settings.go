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
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mscreations/billtracker-plugin/internal/models"
	"github.com/mscreations/billtracker-plugin/internal/scheduler"
	"github.com/mscreations/billtracker-plugin/internal/simplefin"
)

type settingsInstanceRow struct {
	InstanceID       int
	Name             string
	Amount           string
	Due              time.Time
	Paid             bool
	BootstrapManaged bool
}

type settingsBillDefRow struct {
	ID                int
	Name              string
	Amount            string // display, e.g. "$128.43"
	AmountDollars     string // input value, e.g. "128.43"
	ScheduleDesc      string
	ScheduleType      string // "monthly", "quarterly", or "one_off" - for the edit form's <select>
	DayOfMonth        string
	QuarterStartMonth string // "1", "2", or "3" - for quarterly's rotation <select>
	OneOffDate        string // YYYY-MM-DD
	VendorURL         string
	BootstrapManaged  bool
}

type settingsAccountRow struct {
	ID          int
	Name        string
	DisplayName string
	OrgName     string
	Balance     string
	Visible     bool
	IsCredit    bool
}

type settingsPageData struct {
	Instances   []settingsInstanceRow
	Definitions []settingsBillDefRow

	SimpleFinConnected  bool
	SimpleFinLastSynced string
	SimpleFinLastError  string
	Accounts            []settingsAccountRow

	Error   string
	Success string

	// SimpleFinRefreshing is true right after a connect/refresh action kicks
	// off refreshSimpleFinAsync - the sync runs in a background goroutine
	// that outlives the request, so the page rendered in this same response
	// can't show the new accounts yet. The template uses this to schedule a
	// single auto-reload so the accounts appear without the user manually
	// refreshing.
	SimpleFinRefreshing bool
}

// SettingsPage handles GET+POST /settings - the full HTML page hhq's
// ProxySettings relays verbatim from behind its own parent-auth+CSRF
// middleware (this plugin implements no auth of its own). Only ever reached
// through hhq's dashboard at /parent/plugins/bill-tracker/settings, so
// every form in the template MUST use a relative (not absolute) action so
// it re-submits through whatever URL the browser actually has - see
// web/templates/settings.html.
func (a *App) SettingsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var pageErr, pageSuccess string
	var refreshing bool
	if r.Method == http.MethodPost {
		// handleSettingsAction's r.ParseForm() must be the FIRST thing to
		// touch the request's form - net/http caches r.Form after the first
		// parse attempt (even one that errored), so reading r.FormValue
		// before this call would silently swallow a malformed-form error.
		pageErr, pageSuccess = a.handleSettingsAction(ctx, r)
		action := r.FormValue("action")
		refreshing = pageErr == "" && (action == "connect_simplefin" || action == "refresh_simplefin")
	}

	data, err := a.buildSettingsPageData(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data.Error = pageErr
	data.Success = pageSuccess
	data.SimpleFinRefreshing = refreshing

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.Templates.ExecuteTemplate(w, "settings", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (a *App) handleSettingsAction(ctx context.Context, r *http.Request) (errMsg, successMsg string) {
	if err := r.ParseForm(); err != nil {
		return "bad form: " + err.Error(), ""
	}

	switch r.FormValue("action") {
	case "add_bill":
		return a.addBill(ctx, r)
	case "update_bill":
		return a.updateBill(ctx, r)
	case "delete_bill":
		return a.deleteBill(ctx, r)
	case "mark_paid":
		return a.markPaid(ctx, r)
	case "connect_simplefin":
		return a.connectSimpleFin(ctx, r)
	case "disconnect_simplefin":
		return a.disconnectSimpleFin(ctx)
	case "refresh_simplefin":
		return a.refreshSimpleFinNow(ctx)
	case "toggle_account_visibility":
		return a.toggleAccountVisibility(ctx, r)
	case "set_account_display_name":
		return a.setAccountDisplayName(ctx, r)
	default:
		return "unknown action", ""
	}
}

func (a *App) addBill(ctx context.Context, r *http.Request) (string, string) {
	def, err := parseBillForm(r)
	if err != nil {
		return err.Error(), ""
	}
	id, err := a.BillDefs.Create(ctx, def)
	if err != nil {
		return "creating bill: " + err.Error(), ""
	}
	def.ID = id
	if err := scheduler.GenerateInstancesForDefinition(ctx, a.Instances, def, a.Cfg.BillInstanceLookaheadDays); err != nil {
		return "", fmt.Sprintf("bill %q added, but generating upcoming due dates failed: %v", def.Name, err)
	}
	return "", fmt.Sprintf("Added %q.", def.Name)
}

func (a *App) updateBill(ctx context.Context, r *http.Request) (string, string) {
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		return "invalid bill id", ""
	}
	existing, err := a.BillDefs.GetByID(ctx, id)
	if err != nil {
		return err.Error(), ""
	}
	if existing.BootstrapManaged {
		return "this bill is managed by bills.json and can't be edited here", ""
	}

	def, err := parseBillForm(r)
	if err != nil {
		return err.Error(), ""
	}
	if err := a.BillDefs.Update(ctx, id, def); err != nil {
		return "updating bill: " + err.Error(), ""
	}
	def.ID = id
	if err := scheduler.GenerateInstancesForDefinition(ctx, a.Instances, def, a.Cfg.BillInstanceLookaheadDays); err != nil {
		return "", fmt.Sprintf("bill %q updated, but generating upcoming due dates failed: %v", def.Name, err)
	}
	return "", fmt.Sprintf("Updated %q.", def.Name)
}

func (a *App) deleteBill(ctx context.Context, r *http.Request) (string, string) {
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		return "invalid bill id", ""
	}
	existing, err := a.BillDefs.GetByID(ctx, id)
	if err != nil {
		return err.Error(), ""
	}
	if existing.BootstrapManaged {
		return "this bill is managed by bills.json and can't be deleted here", ""
	}
	if err := a.BillDefs.Delete(ctx, id); err != nil {
		return "deleting bill: " + err.Error(), ""
	}
	return "", fmt.Sprintf("Deleted %q.", existing.Name)
}

func (a *App) markPaid(ctx context.Context, r *http.Request) (string, string) {
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		return "invalid instance id", ""
	}
	if err := a.Instances.MarkPaid(ctx, id); err != nil {
		return "marking paid: " + err.Error(), ""
	}
	return "", "Marked paid."
}

// parseBillForm reads the shared add/edit bill form fields.
func parseBillForm(r *http.Request) (models.BillDefinition, error) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return models.BillDefinition{}, fmt.Errorf("name is required")
	}
	amountDollars, err := strconv.ParseFloat(r.FormValue("amount"), 64)
	if err != nil {
		return models.BillDefinition{}, fmt.Errorf("invalid amount")
	}
	amountCents := int(amountDollars*100 + 0.5)

	def := models.BillDefinition{
		Name:        name,
		AmountCents: amountCents,
		VendorURL:   sql.NullString{String: strings.TrimSpace(r.FormValue("vendor_url")), Valid: r.FormValue("vendor_url") != ""},
	}

	switch r.FormValue("schedule") {
	case "monthly":
		day, err := strconv.Atoi(r.FormValue("day_of_month"))
		if err != nil || day < 1 || day > 31 {
			return models.BillDefinition{}, fmt.Errorf("day_of_month must be between 1 and 31")
		}
		def.ScheduleType = models.ScheduleMonthly
		def.DayOfMonth = sql.NullInt16{Int16: int16(day), Valid: true}
	case "quarterly":
		day, err := strconv.Atoi(r.FormValue("day_of_month"))
		if err != nil || day < 1 || day > 31 {
			return models.BillDefinition{}, fmt.Errorf("day_of_month must be between 1 and 31")
		}
		start, err := strconv.Atoi(r.FormValue("quarter_start_month"))
		if err != nil || start < 1 || start > 3 {
			return models.BillDefinition{}, fmt.Errorf("quarter_start_month must be 1, 2, or 3")
		}
		def.ScheduleType = models.ScheduleQuarterly
		def.DayOfMonth = sql.NullInt16{Int16: int16(day), Valid: true}
		def.QuarterStartMonth = sql.NullInt16{Int16: int16(start), Valid: true}
	case "one_off":
		due, err := time.Parse("2006-01-02", r.FormValue("one_off_date"))
		if err != nil {
			return models.BillDefinition{}, fmt.Errorf("one_off_date must be YYYY-MM-DD")
		}
		def.ScheduleType = models.ScheduleOneOff
		def.OneOffDate = sql.NullTime{Time: due, Valid: true}
	default:
		return models.BillDefinition{}, fmt.Errorf("schedule must be 'monthly', 'quarterly', or 'one_off'")
	}

	return def, nil
}

func (a *App) connectSimpleFin(ctx context.Context, r *http.Request) (string, string) {
	token := strings.TrimSpace(r.FormValue("setup_token"))
	if token == "" {
		return "setup token is required", ""
	}

	accessURL, err := simplefin.ClaimSetupToken(ctx, token)
	if err != nil {
		return "connecting to SimpleFIN: " + err.Error(), ""
	}
	encrypted, err := a.Encryptor.Encrypt(accessURL)
	if err != nil {
		return "encrypting access URL: " + err.Error(), ""
	}
	if err := a.SimpleFin.Connect(ctx, encrypted); err != nil {
		return "saving SimpleFIN connection: " + err.Error(), ""
	}

	go a.refreshSimpleFinAsync()

	return "", "SimpleFIN connected - refreshing balances now."
}

func (a *App) disconnectSimpleFin(ctx context.Context) (string, string) {
	if err := a.SimpleFin.Disconnect(ctx); err != nil {
		return "disconnecting: " + err.Error(), ""
	}
	if err := a.Accounts.DeleteAll(ctx); err != nil {
		return "removing synced accounts: " + err.Error(), ""
	}
	return "", "SimpleFIN disconnected."
}

func (a *App) refreshSimpleFinNow(ctx context.Context) (string, string) {
	if _, err := a.SimpleFin.Get(ctx); err != nil {
		return "SimpleFIN is not connected", ""
	}
	go a.refreshSimpleFinAsync()
	return "", "Refreshing balances now."
}

// refreshSimpleFinAsync fires a single refresh outside the request's
// lifetime, mirroring hhq's syncAccountAsync "don't wait for the next
// scheduler tick" pattern. Uses a fresh background context since the
// triggering HTTP request will have returned by the time this runs.
func (a *App) refreshSimpleFinAsync() {
	ctx := context.Background()
	sched := &scheduler.Scheduler{
		Cfg:       a.Cfg,
		BillDefs:  a.BillDefs,
		Instances: a.Instances,
		Accounts:  a.Accounts,
		SimpleFin: a.SimpleFin,
		Encryptor: a.Encryptor,
	}
	sched.RefreshSimpleFinNow(ctx)
}

func (a *App) toggleAccountVisibility(ctx context.Context, r *http.Request) (string, string) {
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		return "invalid account id", ""
	}
	visible := r.FormValue("visible") == "true"
	if err := a.Accounts.SetVisible(ctx, id, visible); err != nil {
		return "toggling account: " + err.Error(), ""
	}
	return "", ""
}

func (a *App) setAccountDisplayName(ctx context.Context, r *http.Request) (string, string) {
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		return "invalid account id", ""
	}
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	if err := a.Accounts.SetDisplayName(ctx, id, displayName); err != nil {
		return "setting display name: " + err.Error(), ""
	}
	return "", ""
}

func (a *App) buildSettingsPageData(ctx context.Context) (settingsPageData, error) {
	today := time.Now().Truncate(24 * time.Hour)
	windowEnd := today.AddDate(0, 0, 90)

	instances, err := a.Instances.ListAllForSettings(ctx, today.AddDate(0, 0, -30), windowEnd)
	if err != nil {
		return settingsPageData{}, err
	}
	defs, err := a.BillDefs.ListAll(ctx)
	if err != nil {
		return settingsPageData{}, err
	}
	bootstrapByID := map[int]bool{}
	for _, d := range defs {
		bootstrapByID[d.ID] = d.BootstrapManaged
	}

	var data settingsPageData
	for _, inst := range instances {
		data.Instances = append(data.Instances, settingsInstanceRow{
			InstanceID:       inst.InstanceID,
			Name:             inst.Name,
			Amount:           formatCents(inst.AmountCents),
			Due:              inst.DueDate,
			Paid:             inst.Paid,
			BootstrapManaged: bootstrapByID[inst.DefinitionID],
		})
	}
	for _, def := range defs {
		row := settingsBillDefRow{
			ID:               def.ID,
			Name:             def.Name,
			Amount:           formatCents(def.AmountCents),
			AmountDollars:    formatCentsPlain(def.AmountCents),
			ScheduleDesc:     scheduleDescription(def),
			ScheduleType:     string(def.ScheduleType),
			VendorURL:        def.VendorURL.String,
			BootstrapManaged: def.BootstrapManaged,
		}
		if def.DayOfMonth.Valid {
			row.DayOfMonth = strconv.Itoa(int(def.DayOfMonth.Int16))
		}
		if def.QuarterStartMonth.Valid {
			row.QuarterStartMonth = strconv.Itoa(int(def.QuarterStartMonth.Int16))
		}
		if def.OneOffDate.Valid {
			row.OneOffDate = def.OneOffDate.Time.Format("2006-01-02")
		}
		data.Definitions = append(data.Definitions, row)
	}

	if conn, err := a.SimpleFin.Get(ctx); err == nil {
		data.SimpleFinConnected = true
		if conn.LastSyncedAt.Valid {
			data.SimpleFinLastSynced = conn.LastSyncedAt.Time.Format("2006-01-02 15:04:05")
		}
		data.SimpleFinLastError = conn.LastSyncError.String

		accounts, err := a.Accounts.ListAll(ctx)
		if err != nil {
			return settingsPageData{}, err
		}
		var bankRows, creditRows []settingsAccountRow
		for _, acc := range accounts {
			row := settingsAccountRow{
				ID:          acc.ID,
				Name:        acc.Name,
				DisplayName: acc.DisplayName.String,
				OrgName:     acc.OrgName.String,
				Balance:     formatCents(int(acc.BalanceCents)),
				Visible:     acc.Visible,
				IsCredit:    acc.IsCredit(),
			}
			if row.IsCredit {
				creditRows = append(creditRows, row)
			} else {
				bankRows = append(bankRows, row)
			}
		}
		data.Accounts = append(bankRows, creditRows...)
	} else if err != models.ErrNoSimpleFinConnection {
		return settingsPageData{}, err
	}

	return data, nil
}

var quarterRotationMonths = map[int16]string{
	1: "Jan/Apr/Jul/Oct",
	2: "Feb/May/Aug/Nov",
	3: "Mar/Jun/Sep/Dec",
}

func scheduleDescription(def models.BillDefinition) string {
	if def.ScheduleType == models.ScheduleOneOff && def.OneOffDate.Valid {
		return "One-off: " + def.OneOffDate.Time.Format("2006-01-02")
	}
	if def.ScheduleType == models.ScheduleMonthly && def.DayOfMonth.Valid {
		return fmt.Sprintf("Monthly on day %d", def.DayOfMonth.Int16)
	}
	if def.ScheduleType == models.ScheduleQuarterly && def.DayOfMonth.Valid && def.QuarterStartMonth.Valid {
		return fmt.Sprintf("Quarterly (%s) on day %d", quarterRotationMonths[def.QuarterStartMonth.Int16], def.DayOfMonth.Int16)
	}
	return "Unknown schedule"
}
