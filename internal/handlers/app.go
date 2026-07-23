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

// Package handlers implements the Bill Tracker plugin's HTTP contract with
// hhq: GET /manifest, GET /view, GET /events, GET+POST /settings, GET
// /healthz - see internal/plugins in the hhq repo for the host-side client.
package handlers

import (
	"html/template"

	"github.com/mscreations/billtracker-plugin/internal/config"
	"github.com/mscreations/billtracker-plugin/internal/models"
	"github.com/mscreations/billtracker-plugin/internal/util"
)

// App holds every dependency a handler might need. ENCRYPTION_KEY is
// required (see config.Load), so Encryptor is always set on any App built
// via the real startup path in cmd/server/main.go.
type App struct {
	Cfg *config.Config

	BillDefs  *models.BillDefinitionStore
	Instances *models.BillInstanceStore
	Accounts  *models.AccountStore
	Settings  *models.SettingsStore
	SimpleFin *models.SimpleFinConnectionStore
	Vendors   *models.VendorConnectionStore

	Encryptor *util.Encryptor
	Templates *template.Template
}
