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

package main

import (
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mscreations/billtracker-plugin/internal/config"
	"github.com/mscreations/billtracker-plugin/internal/handlers"
	"github.com/mscreations/billtracker-plugin/internal/models"
	"github.com/mscreations/billtracker-plugin/internal/testutil"
	"github.com/mscreations/billtracker-plugin/internal/util"
	"github.com/mscreations/billtracker-plugin/web"
)

func newTestApp(t *testing.T) (*handlers.App, *config.Config) {
	t.Helper()
	conn := testutil.RequireDB(t)
	encryptor, err := util.NewEncryptor(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	tmpl, err := template.ParseFS(web.TemplatesFS, "templates/*.html", "templates/*/*.html")
	if err != nil {
		t.Fatalf("parsing templates: %v", err)
	}
	cfg := &config.Config{BillInstanceLookaheadDays: 60}
	app := &handlers.App{
		Cfg:       cfg,
		BillDefs:  &models.BillDefinitionStore{DB: conn},
		Instances: &models.BillInstanceStore{DB: conn},
		Accounts:  &models.AccountStore{DB: conn},
		Settings:  &models.SettingsStore{DB: conn},
		SimpleFin: &models.SimpleFinConnectionStore{DB: conn},
		Vendors:   &models.VendorConnectionStore{DB: conn},
		Encryptor: encryptor,
		Templates: tmpl,
	}
	return app, cfg
}

func TestBootstrapBillsMissingFileIsNoOp(t *testing.T) {
	app, cfg := newTestApp(t)
	cfg.ConfigDir = t.TempDir() // no bills.json in here

	bootstrapBills(t.Context(), app, cfg)

	defs, err := app.BillDefs.ListAll(t.Context())
	if err != nil || len(defs) != 0 {
		t.Fatalf("expected no bills, got %+v err=%v", defs, err)
	}
}

func TestBootstrapBillsUnreadableFileIsNoOp(t *testing.T) {
	app, cfg := newTestApp(t)
	dir := t.TempDir()
	cfg.ConfigDir = dir
	// A directory named bills.json makes os.ReadFile fail with something
	// other than os.IsNotExist - exercises the "reading" error branch
	// distinct from the not-exist branch.
	if err := os.Mkdir(filepath.Join(dir, "bills.json"), 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	bootstrapBills(t.Context(), app, cfg)

	defs, err := app.BillDefs.ListAll(t.Context())
	if err != nil || len(defs) != 0 {
		t.Fatalf("expected no bills, got %+v err=%v", defs, err)
	}
}

func TestBootstrapBillsInvalidJSONIsNoOp(t *testing.T) {
	app, cfg := newTestApp(t)
	dir := t.TempDir()
	cfg.ConfigDir = dir
	if err := os.WriteFile(filepath.Join(dir, "bills.json"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	bootstrapBills(t.Context(), app, cfg)

	defs, err := app.BillDefs.ListAll(t.Context())
	if err != nil || len(defs) != 0 {
		t.Fatalf("expected no bills, got %+v err=%v", defs, err)
	}
}

func TestBootstrapBillsValidJSONCreatesBills(t *testing.T) {
	app, cfg := newTestApp(t)
	dir := t.TempDir()
	cfg.ConfigDir = dir
	json := `[{"name":"Rent","amount":1500,"schedule":"monthly","day_of_month":1}]`
	if err := os.WriteFile(filepath.Join(dir, "bills.json"), []byte(json), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	bootstrapBills(t.Context(), app, cfg)

	defs, err := app.BillDefs.ListAll(t.Context())
	if err != nil || len(defs) != 1 {
		t.Fatalf("expected 1 bill, got %+v err=%v", defs, err)
	}
}
