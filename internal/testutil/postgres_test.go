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

package testutil

import "testing"

// TestRequireDBReturnsAMigratedUsableConnection covers RequireDB's happy
// path: starting (or reusing) the shared container, returning a usable
// *sql.DB, and registering the truncate-after-test cleanup.
func TestRequireDBReturnsAMigratedUsableConnection(t *testing.T) {
	conn := RequireDB(t)
	if err := conn.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	var tableName string
	err := conn.QueryRow(`
		SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename = 'bt_settings'`).Scan(&tableName)
	if err != nil {
		t.Fatalf("expected the migrated bt_settings table to exist: %v", err)
	}
}

// TestRequireDBTruncatesBetweenTests confirms the t.Cleanup registered by
// RequireDB actually empties app tables between tests - this test relies on
// running after TestRequireDBSeedsARowForTruncationCoverage (Go runs tests
// within a file in source order) to see an empty table left by that test's
// cleanup.
func TestRequireDBSeedsARowForTruncationCoverage(t *testing.T) {
	conn := RequireDB(t)
	if _, err := conn.Exec(`INSERT INTO bt_settings (key, value) VALUES ('truncation-probe', 'x')`); err != nil {
		t.Fatalf("seeding row: %v", err)
	}
}

func TestRequireDBTruncatesBetweenTests(t *testing.T) {
	conn := RequireDB(t)
	var count int
	if err := conn.QueryRow(`SELECT count(*) FROM bt_settings WHERE key = 'truncation-probe'`).Scan(&count); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected the prior test's row to have been truncated away, found %d", count)
	}
}
