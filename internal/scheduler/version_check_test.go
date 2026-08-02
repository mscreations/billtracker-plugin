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

package scheduler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mscreations/billtracker-plugin/internal/release"
)

func TestCheckVersionPopulatesCacheFromFakeGitHub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v1.5.0", "body": "feat: new release"})
	}))
	t.Cleanup(srv.Close)

	origBase := release.GitHubAPIBase
	release.GitHubAPIBase = srv.URL
	t.Cleanup(func() { release.GitHubAPIBase = origBase })

	s := &Scheduler{Version: "1.4.0", Releases: &release.Cache{}}
	s.checkVersion(t.Context())

	info, ok := s.Releases.Get()
	if !ok {
		t.Fatal("expected a cached version info")
	}
	if info.Version != "1.5.0" || info.Changelog != "feat: new release" {
		t.Errorf("got %+v", info)
	}
}

func TestCheckVersionLeavesCacheUntouchedOnFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	origBase := release.GitHubAPIBase
	release.GitHubAPIBase = srv.URL
	t.Cleanup(func() { release.GitHubAPIBase = origBase })

	cache := &release.Cache{}
	cache.Set(&release.Info{Version: "1.3.0"})
	s := &Scheduler{Version: "1.4.0", Releases: cache}
	s.checkVersion(t.Context())

	info, ok := cache.Get()
	if !ok || info.Version != "1.3.0" {
		t.Fatalf("expected the prior cached value to survive a fetch error, got %+v, %v", info, ok)
	}
}
