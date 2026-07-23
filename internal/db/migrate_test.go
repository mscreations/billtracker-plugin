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

package db_test

import (
	"database/sql"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/mscreations/billtracker-plugin/internal/db"
	"github.com/mscreations/billtracker-plugin/internal/testutil"
)

// TestMigrateErrorsWhenConnectionUnusable covers Migrate's goose.Up-error
// branch: unlike New (which pings first), Migrate is handed a *sql.DB
// directly with no connectivity check of its own, so an unusable connection
// must still surface as a wrapped error rather than panicking or hanging.
func TestMigrateErrorsWhenConnectionUnusable(t *testing.T) {
	conn, err := sql.Open("pgx", "postgres://user:pass@127.0.0.1:1/nonexistent?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer conn.Close()

	err = db.Migrate(conn)
	if err == nil {
		t.Fatal("expected Migrate to fail against an unreachable connection")
	}
	if !strings.Contains(err.Error(), "running migrations") {
		t.Fatalf("error = %v, want it to include the 'running migrations' wrap context", err)
	}
}

// TestMigrateIsIdempotent confirms Migrate can be safely re-run against an
// already-migrated database - main.go calls it unconditionally on every
// startup, relying on goose's own idempotency. The second call must
// succeed with nothing pending to apply.
func TestMigrateIsIdempotent(t *testing.T) {
	conn := testutil.RequireDB(t)
	if err := db.Migrate(conn); err != nil {
		t.Fatalf("Migrate (second run): %v", err)
	}
}
