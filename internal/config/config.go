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

// Package config centralizes all environment-driven configuration for the
// Bill Tracker plugin, read the same way as hhq itself: Getenv checks
// KEY_FILE first (for Kubernetes Secret-mounted files), then the plain KEY
// env var, then a caller-supplied default.
package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	// HTTP
	ListenAddr string // e.g. ":8090" - no CLI flags, env-only like hhq

	// Postgres connection. Same database hhq itself uses in this deployment,
	// but this app must never assume that's guaranteed in production - all
	// plugin state lives in its own bt_-prefixed tables (see internal/db),
	// never reading hhq's own tables directly.
	DBHost     string
	DBPort     string
	DBName     string
	DBUser     string
	DBPassword string
	DBSSLMode  string

	// ConfigDir is scanned for bills.json on startup (see
	// internal/config/bills_bootstrap.go).
	ConfigDir string

	// EncryptionKey is a hex-encoded 32-byte AES-256 key (internal/util.
	// NewEncryptor). Required - used both for SimpleFIN access URLs and to
	// encrypt the shared token this plugin issues hhq on self-registration
	// at rest (see internal/handlers.Register).
	EncryptionKey string

	// BillInstanceLookaheadDays controls how far ahead recurring bill
	// instances are generated (see internal/scheduler).
	BillInstanceLookaheadDays int

	// SimpleFinRefreshInterval controls how often account balances are
	// re-fetched from the configured SimpleFIN Bridge connection.
	SimpleFinRefreshIntervalMinutes int

	// VendorRefreshIntervalMinutes controls how often bills with a vendor
	// connector attached (see internal/connectors) are re-fetched from the
	// vendor's own bill-pay portal. Defaults far longer than the SimpleFIN
	// interval since bill amounts/due dates change far less often than
	// account balances, and logging into a vendor's site is a heavier,
	// more failure-prone operation worth not hammering.
	VendorRefreshIntervalMinutes int
}

func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr: getEnvDefault("LISTEN_ADDR", ":8090"),

		DBHost:     Getenv("DB_HOST"),
		DBPort:     getEnvDefault("DB_PORT", "5432"),
		DBName:     Getenv("DB_NAME"),
		DBUser:     Getenv("DB_USER"),
		DBPassword: Getenv("DB_PASSWORD"),
		DBSSLMode:  getEnvDefault("DB_SSLMODE", "disable"),

		ConfigDir: getEnvDefault("CONFIG_DIR", "./.config"),

		EncryptionKey: Getenv("ENCRYPTION_KEY"),
	}

	var err error
	cfg.BillInstanceLookaheadDays, err = strconv.Atoi(getEnvDefault("BILL_INSTANCE_LOOKAHEAD_DAYS", "60"))
	if err != nil {
		return nil, fmt.Errorf("invalid BILL_INSTANCE_LOOKAHEAD_DAYS: %w", err)
	}

	cfg.SimpleFinRefreshIntervalMinutes, err = strconv.Atoi(getEnvDefault("SIMPLEFIN_REFRESH_INTERVAL_MINUTES", "60"))
	if err != nil {
		return nil, fmt.Errorf("invalid SIMPLEFIN_REFRESH_INTERVAL_MINUTES: %w", err)
	}

	cfg.VendorRefreshIntervalMinutes, err = strconv.Atoi(getEnvDefault("VENDOR_REFRESH_INTERVAL_MINUTES", "360"))
	if err != nil {
		return nil, fmt.Errorf("invalid VENDOR_REFRESH_INTERVAL_MINUTES: %w", err)
	}

	if cfg.DBHost == "" || cfg.DBName == "" || cfg.DBUser == "" {
		return nil, fmt.Errorf("DB_HOST, DB_NAME, and DB_USER must be set")
	}
	if cfg.EncryptionKey == "" {
		return nil, fmt.Errorf("ENCRYPTION_KEY must be set - it protects the shared token issued to hhq on self-registration (and SimpleFIN access URLs, if used), see README.md")
	}

	return cfg, nil
}

// DSN builds a libpq-style connection string for pgx.
func (c *Config) DSN() string {
	return fmt.Sprintf("host=%s port=%s dbname=%s user=%s password=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBName, c.DBUser, c.DBPassword, c.DBSSLMode)
}

func getEnvDefault(key, def string) string {
	if v := Getenv(key); v != "" {
		return v
	}
	return def
}

// Getenv reads KEY_FILE (a path to a file whose contents are the value) if
// set, otherwise falls back to the plain KEY env var. Matches hhq's own
// internal/config.Getenv exactly, so both apps behave identically for
// Kubernetes Secret-mounted config.
func Getenv(key string) string {
	if path := os.Getenv(key + "_FILE"); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			return string(data)
		}
	}
	return os.Getenv(key)
}
