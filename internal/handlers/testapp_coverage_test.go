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
	"html/template"
	"strings"
	"testing"

	"github.com/mscreations/billtracker-plugin/internal/config"
	"github.com/mscreations/billtracker-plugin/internal/models"
	"github.com/mscreations/billtracker-plugin/internal/testutil"
	"github.com/mscreations/billtracker-plugin/internal/util"
	"github.com/mscreations/billtracker-plugin/web"
)

// newFullTestApp builds an *App with every store wired to a real, migrated
// Postgres (see testutil.RequireDB) plus real embedded templates - used by
// every handler test in this package that needs more than register_test.go's
// minimal newTestApp (which only wires Settings+Encryptor).
func newFullTestApp(t *testing.T) *App {
	t.Helper()
	conn := testutil.RequireDB(t)
	encryptor, err := util.NewEncryptor(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	tmpl, err := template.ParseFS(web.TemplatesFS, "templates/*.html", "templates/*/*.html")
	if err != nil {
		t.Fatalf("parsing templates: %v", err)
	}
	return &App{
		Cfg: &config.Config{
			BillInstanceLookaheadDays: 60,
		},
		BillDefs:  &models.BillDefinitionStore{DB: conn},
		Instances: &models.BillInstanceStore{DB: conn},
		Accounts:  &models.AccountStore{DB: conn},
		Settings:  &models.SettingsStore{DB: conn},
		SimpleFin: &models.SimpleFinConnectionStore{DB: conn},
		Vendors:   &models.VendorConnectionStore{DB: conn},
		Encryptor: encryptor,
		Templates: tmpl,
	}
}
