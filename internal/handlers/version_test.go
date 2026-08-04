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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mscreations/billtracker-plugin/internal/release"
)

func TestGetVersionDefaultResponseBeforeAnyPoll(t *testing.T) {
	a := &App{Version: "1.2.0", Releases: &release.Cache{}}

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	a.GetVersion(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp versionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Version != "1.2.0" || resp.UpgradeAvailable || resp.Channel != "release" {
		t.Errorf("got %+v", resp)
	}
	if resp.Checked {
		t.Error("expected Checked=false before any poll has completed")
	}
}

func TestGetVersionReportsUpgradeAvailableFromCache(t *testing.T) {
	cache := &release.Cache{}
	cache.Set(&release.Info{Version: "1.3.0", Changelog: "feat: something new"})
	a := &App{Version: "1.2.0", Releases: cache}

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	a.GetVersion(rec, req)

	var resp versionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if !resp.UpgradeAvailable || resp.UpgradeVersion != "1.3.0" || resp.Changelog != "feat: something new" {
		t.Errorf("got %+v", resp)
	}
	if !resp.Checked {
		t.Error("expected Checked=true once the cache has a value")
	}
}

func TestGetVersionReportsDevChannelAndNoUpgradeWhenCacheNotNewer(t *testing.T) {
	cache := &release.Cache{}
	cache.Set(&release.Info{Version: "1.2.0-dev", Changelog: "old news"})
	a := &App{Version: "1.2.0-dev", Releases: cache}

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	a.GetVersion(rec, req)

	var resp versionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Channel != "dev" {
		t.Errorf("Channel = %q, want dev", resp.Channel)
	}
	if resp.UpgradeAvailable {
		t.Error("did not expect an upgrade when the cached version equals the running version")
	}
}

func TestGetVersionHandlesNilReleasesCacheGracefully(t *testing.T) {
	a := &App{Version: "1.2.0"}

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	a.GetVersion(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp versionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.UpgradeAvailable {
		t.Error("expected no upgrade reported when Releases is nil (nothing polled yet)")
	}
}
