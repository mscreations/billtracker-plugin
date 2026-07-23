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

// Package testutil provides shared test helpers, primarily a real, migrated
// Postgres instance backed by testcontainers-go - mirrors hhq's own
// internal/testutil package exactly (see that repo's CLAUDE.md rounds 3 and
// 9 for why: this plugin's SQL, like hhq's, relies on Postgres-specific
// behavior a generic database/sql mock can't faithfully exercise).
package testutil

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/mscreations/billtracker-plugin/internal/db"
)

// One Postgres container is started per test binary and reused across every
// test in that package; isolation between tests comes from truncating all
// app tables after each test (see RequireDB), not from a fresh container.
var (
	once      sync.Once
	sharedDB  *sql.DB
	sharedErr error
)

// postgresImage is a package-level var (rather than an inline literal in
// startContainer) so a test can point it at a deliberately-nonexistent
// image to exercise startContainer's error branch without needing to break
// Docker itself.
var postgresImage = "postgres:16-alpine"

// RequireDB skips the test unless Docker is available. It returns a
// migrated *sql.DB shared with other tests in the same package, and
// registers a cleanup that truncates all app tables after this test so the
// next test starts from empty tables.
func RequireDB(t *testing.T) *sql.DB {
	t.Helper()

	if err := checkDockerAvailable(); err != nil {
		t.Skipf("skipping: docker not available for testcontainers: %v", err)
	}

	once.Do(func() {
		sharedDB, sharedErr = startContainer()
	})
	if sharedErr != nil {
		t.Fatalf("starting shared test postgres: %v", sharedErr)
	}

	t.Cleanup(func() {
		if err := truncateAll(sharedDB); err != nil {
			t.Logf("truncating tables after test: %v", err)
		}
	})

	return sharedDB
}

func startContainer() (*sql.DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := tcpostgres.Run(ctx,
		postgresImage,
		tcpostgres.WithDatabase("billtracker_test"),
		tcpostgres.WithUsername("billtracker"),
		tcpostgres.WithPassword("billtracker"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("starting postgres container: %w", err)
	}
	// Deliberately not terminated here: it lives for the lifetime of the
	// test binary process. testcontainers' Ryuk reaper cleans it up after
	// this process exits.

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("getting connection string: %w", err)
	}

	conn, err := db.New(dsn)
	if err != nil {
		return nil, fmt.Errorf("connecting to test postgres: %w", err)
	}

	if err := db.Migrate(conn); err != nil {
		return nil, fmt.Errorf("running migrations against test postgres: %w", err)
	}

	return conn, nil
}

// truncateAll clears every app table (everything except goose's own
// bookkeeping table) and resets identity sequences.
func truncateAll(conn *sql.DB) error {
	rows, err := conn.Query(`
		SELECT tablename FROM pg_tables
		WHERE schemaname = 'public' AND tablename NOT IN ('bt_goose_db_version')`)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	if len(tables) == 0 {
		return nil
	}

	query := "TRUNCATE TABLE"
	for i, tbl := range tables {
		if i > 0 {
			query += ","
		}
		query += " " + tbl
	}
	query += " RESTART IDENTITY CASCADE"

	_, err = conn.Exec(query)
	return err
}

func checkDockerAvailable() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return err
	}
	defer provider.Close()
	return provider.Health(ctx)
}
