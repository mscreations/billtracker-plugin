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

package config

import (
	"os"
	"strings"
	"testing"
)

// setRequiredEnv sets exactly the env vars Load() requires to succeed, using
// t.Setenv so each var is automatically restored after the test - callers
// can then unset or override individual vars to exercise validation.
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DB_HOST", "localhost")
	t.Setenv("DB_NAME", "billtracker")
	t.Setenv("DB_USER", "billtracker")
	t.Setenv("ENCRYPTION_KEY", strings.Repeat("ab", 32))
}

func TestLoadSucceedsWithAllRequiredVarsSet(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DBHost != "localhost" || cfg.DBName != "billtracker" || cfg.DBUser != "billtracker" {
		t.Fatalf("unexpected db config: %+v", cfg)
	}
	if cfg.EncryptionKey != strings.Repeat("ab", 32) {
		t.Fatalf("unexpected encryption key: %q", cfg.EncryptionKey)
	}
	// Defaults applied when not overridden.
	if cfg.ListenAddr != ":8090" {
		t.Errorf("ListenAddr = %q, want default :8090", cfg.ListenAddr)
	}
	if cfg.DBPort != "5432" {
		t.Errorf("DBPort = %q, want default 5432", cfg.DBPort)
	}
	if cfg.DBSSLMode != "disable" {
		t.Errorf("DBSSLMode = %q, want default disable", cfg.DBSSLMode)
	}
	if cfg.ConfigDir != "./.config" {
		t.Errorf("ConfigDir = %q, want default ./.config", cfg.ConfigDir)
	}
	if cfg.BillInstanceLookaheadDays != 60 {
		t.Errorf("BillInstanceLookaheadDays = %d, want default 60", cfg.BillInstanceLookaheadDays)
	}
	if cfg.SimpleFinRefreshIntervalMinutes != 60 {
		t.Errorf("SimpleFinRefreshIntervalMinutes = %d, want default 60", cfg.SimpleFinRefreshIntervalMinutes)
	}
	if cfg.VendorRefreshIntervalMinutes != 360 {
		t.Errorf("VendorRefreshIntervalMinutes = %d, want default 360", cfg.VendorRefreshIntervalMinutes)
	}
}

// TestLoadRequiresEachDBVarIndividually mirrors hhq's own config test
// pattern: each of DB_HOST/DB_NAME/DB_USER must independently be required,
// not just "at least one of them."
func TestLoadRequiresEachDBVarIndividually(t *testing.T) {
	for _, missing := range []string{"DB_HOST", "DB_NAME", "DB_USER"} {
		t.Run(missing, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(missing, "")

			_, err := Load()
			if err == nil {
				t.Fatalf("expected Load to fail with %s unset", missing)
			}
		})
	}
}

// TestLoadRequiresEncryptionKey is a direct regression test for the plugin's
// self-registration handshake: this plugin now requires ENCRYPTION_KEY
// unconditionally (not just for SimpleFIN), since the shared token it issues
// hhq on first contact is encrypted at rest with it (see internal/handlers.
// Register) - the app must refuse to start rather than silently store that
// token in plaintext.
func TestLoadRequiresEncryptionKey(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("ENCRYPTION_KEY", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected Load to fail with ENCRYPTION_KEY unset")
	}
	if !strings.Contains(err.Error(), "ENCRYPTION_KEY") {
		t.Errorf("error message should mention ENCRYPTION_KEY, got: %v", err)
	}
}

func TestLoadRejectsInvalidIntegerEnvVars(t *testing.T) {
	cases := []struct {
		name   string
		envVar string
	}{
		{"lookahead days", "BILL_INSTANCE_LOOKAHEAD_DAYS"},
		{"simplefin interval", "SIMPLEFIN_REFRESH_INTERVAL_MINUTES"},
		{"vendor interval", "VENDOR_REFRESH_INTERVAL_MINUTES"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(tc.envVar, "not-a-number")

			_, err := Load()
			if err == nil {
				t.Fatalf("expected Load to fail with %s=not-a-number", tc.envVar)
			}
		})
	}
}

// TestGetenvFileSuffixTakesPrecedence is a direct regression test for the
// Kubernetes Secret-mounted-file convention shared with hhq's own
// internal/config.Getenv: KEY_FILE, when set, must win over a plain KEY env
// var, not just be a fallback for when KEY is unset.
func TestGetenvFileSuffixTakesPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/secret"
	if err := writeFile(path, "from-file"); err != nil {
		t.Fatalf("writing test secret file: %v", err)
	}

	t.Setenv("MY_KEY", "from-plain-env")
	t.Setenv("MY_KEY_FILE", path)

	if got := Getenv("MY_KEY"); got != "from-file" {
		t.Errorf("Getenv(MY_KEY) = %q, want %q (file should take precedence)", got, "from-file")
	}
}

func TestGetenvFallsBackToPlainVarWhenFileUnset(t *testing.T) {
	t.Setenv("MY_KEY2", "from-plain-env")

	if got := Getenv("MY_KEY2"); got != "from-plain-env" {
		t.Errorf("Getenv(MY_KEY2) = %q, want %q", got, "from-plain-env")
	}
}

func writeFile(path, contents string) error {
	return os.WriteFile(path, []byte(contents), 0o600)
}
