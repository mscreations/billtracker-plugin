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
	"strings"
	"testing"

	"github.com/mscreations/billtracker-plugin/internal/db"
	"github.com/mscreations/billtracker-plugin/internal/testutil"
)

// TestNewErrorsForMalformedDSN covers New's ping-error branch. The pgx
// stdlib driver parses the DSN lazily, at connect time rather than at
// sql.Open time (matching database/sql's own documented "Open may just
// validate its arguments without creating a connection" behavior), so a
// malformed DSN surfaces here as a ping failure, not an Open failure.
func TestNewErrorsForMalformedDSN(t *testing.T) {
	_, err := db.New("this is not a valid postgres dsn")
	if err == nil {
		t.Fatal("expected New to fail for a malformed DSN")
	}
	if !strings.Contains(err.Error(), "pinging db") {
		t.Fatalf("error = %v, want it to include the 'pinging db' wrap context", err)
	}
}

// TestNewErrorsForUnreachableHost covers the same ping-error branch via a
// syntactically valid DSN pointing at nothing listening. connect_timeout=1
// keeps this fast rather than relying on New's full 5s ping timeout.
func TestNewErrorsForUnreachableHost(t *testing.T) {
	_, err := db.New("postgres://user:pass@127.0.0.1:1/nonexistent?sslmode=disable&connect_timeout=1")
	if err == nil {
		t.Fatal("expected New to fail for an unreachable host")
	}
	if !strings.Contains(err.Error(), "pinging db") {
		t.Fatalf("error = %v, want it to include the 'pinging db' wrap context", err)
	}
}

// TestNewSucceedsAgainstARealDatabase covers New's happy path (Open, pool
// settings, ping) end to end. testutil.RequireDB provisions a real
// Postgres container via db.New + db.Migrate internally (see
// internal/testutil/postgres.go's startContainer), so this exercises the
// exact same compiled db.New/db.Migrate code this package's coverage is
// measured against - it's not a redundant/separate DB, just routed through
// the shared test helper rather than duplicating container setup here.
func TestNewSucceedsAgainstARealDatabase(t *testing.T) {
	conn := testutil.RequireDB(t)
	if err := conn.Ping(); err != nil {
		t.Fatalf("Ping on the connection returned by testutil.RequireDB: %v", err)
	}
}
