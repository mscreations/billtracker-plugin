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
	"fmt"

	"github.com/mscreations/billtracker-plugin/internal/config"
	"github.com/mscreations/billtracker-plugin/internal/logging"
	"github.com/mscreations/billtracker-plugin/internal/models"
)

// BootstrapVendorConnections reconciles bt_vendor_connections against
// VENDOR_CONNECTIONS(_FILE) entries: each entry must name an existing bill
// (created via bills.json or the settings UI) and is upserted onto that
// bill's connection row every startup, same "config file is the standing
// source of truth" reconciliation as hhq's BootstrapCalendarAccounts.
func (a *App) BootstrapVendorConnections(ctx context.Context, entries []config.VendorConnectionBootstrap) {
	if len(entries) == 0 {
		return
	}

	for _, entry := range entries {
		if err := a.bootstrapVendorConnection(ctx, entry); err != nil {
			logging.Errorf("bootstrap: vendor connection for bill %q: %v", entry.BillName, err)
		}
	}
}

func (a *App) bootstrapVendorConnection(ctx context.Context, entry config.VendorConnectionBootstrap) error {
	if entry.BillName == "" || entry.Connector == "" || entry.Username == "" || entry.Password == "" {
		return fmt.Errorf("bill_name, connector, username, and password are all required")
	}

	def, err := a.BillDefs.GetByName(ctx, entry.BillName)
	if err != nil {
		if err == models.ErrBillNotFound {
			return fmt.Errorf("no bill named %q exists (vendor connections attach to an existing bill, they don't create one)", entry.BillName)
		}
		return fmt.Errorf("looking up bill %q: %w", entry.BillName, err)
	}

	encryptedPassword, err := a.Encryptor.Encrypt(entry.Password)
	if err != nil {
		return fmt.Errorf("encrypting password: %w", err)
	}

	if _, err := a.Vendors.Upsert(ctx, models.VendorConnection{
		BillDefinitionID:  def.ID,
		Connector:         entry.Connector,
		Tenant:            entry.Tenant,
		Username:          entry.Username,
		EncryptedPassword: encryptedPassword,
		BootstrapManaged:  true,
	}); err != nil {
		return fmt.Errorf("upserting: %w", err)
	}

	logging.Infof("bootstrap: vendor connection %q (%s) attached to bill %q", entry.Connector, entry.Tenant, entry.BillName)
	return nil
}
