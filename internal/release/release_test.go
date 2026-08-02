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

package release

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func withFakeGitHub(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	orig := GitHubAPIBase
	GitHubAPIBase = srv.URL
	t.Cleanup(func() { GitHubAPIBase = orig })
}

func TestCheckForUpdateSelectsReleasesEndpointForNonDevVersion(t *testing.T) {
	var gotPath string
	withFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v1.2.0", Body: "feat: new stuff"})
	})

	info, err := CheckForUpdate(t.Context(), "mscreations/billtracker-plugin", "1.1.0")
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/releases/latest") {
		t.Errorf("request path = %q, want suffix /releases/latest", gotPath)
	}
	if info.Version != "1.2.0" {
		t.Errorf("Version = %q, want %q", info.Version, "1.2.0")
	}
	if info.Changelog != "feat: new stuff" {
		t.Errorf("Changelog = %q, want %q", info.Changelog, "feat: new stuff")
	}
}

func TestCheckForUpdateReleaseFallsBackToNameWhenBodyEmpty(t *testing.T) {
	withFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v1.2.0", Name: "Version 1.2.0"})
	})

	info, err := CheckForUpdate(t.Context(), "mscreations/billtracker-plugin", "1.1.0")
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if info.Changelog != "Version 1.2.0" {
		t.Errorf("Changelog = %q, want %q", info.Changelog, "Version 1.2.0")
	}
}

func TestCheckForUpdateSelectsTagsEndpointForDevVersionAndFetchesCommitSubject(t *testing.T) {
	var sawTagsRequest, sawCommitRequest bool
	withFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tags"):
			sawTagsRequest = true
			tag := githubTag{Name: "v1.4.0-dev"}
			tag.Commit.SHA = "abc123"
			_ = json.NewEncoder(w).Encode([]githubTag{tag})
		case strings.Contains(r.URL.Path, "/commits/"):
			sawCommitRequest = true
			var c githubCommit
			c.Commit.Message = "feat: Update versioning\n\nlonger body here"
			_ = json.NewEncoder(w).Encode(c)
		}
	})

	info, err := CheckForUpdate(t.Context(), "mscreations/billtracker-plugin", "1.3.0-dev")
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if !sawTagsRequest {
		t.Fatal("expected a request to the tags endpoint")
	}
	if !sawCommitRequest {
		t.Fatal("expected a request to the commit endpoint for the changelog")
	}
	if info.Version != "1.4.0-dev" {
		t.Errorf("Version = %q, want %q", info.Version, "1.4.0-dev")
	}
	if info.Changelog != "feat: Update versioning" {
		t.Errorf("Changelog = %q, want only the first line of the commit message", info.Changelog)
	}
}

func TestCheckForUpdateDevTagsIgnoresNonDevTags(t *testing.T) {
	withFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tags") {
			// A release tag (no -dev suffix) with a higher version must be
			// ignored when checking the dev channel - only -dev tags count.
			_ = json.NewEncoder(w).Encode([]githubTag{{Name: "v9.9.9"}, {Name: "v1.4.0-dev"}})
		}
	})

	info, err := CheckForUpdate(t.Context(), "mscreations/billtracker-plugin", "1.3.0-dev")
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if info.Version != "1.4.0-dev" {
		t.Errorf("Version = %q, want %q (non-dev tags must be ignored)", info.Version, "1.4.0-dev")
	}
}

func TestCheckForUpdateDevTagsErrorsWhenNoneFound(t *testing.T) {
	withFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]githubTag{{Name: "v9.9.9"}})
	})

	if _, err := CheckForUpdate(t.Context(), "mscreations/billtracker-plugin", "1.3.0-dev"); err == nil {
		t.Fatal("expected an error when no dev-versioned tags are found")
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want             bool
	}{
		{"1.2.0", "1.3.0", true},
		{"1.3.0", "1.2.0", false},
		{"1.2.0", "1.2.0", false},
		{"1.2.3-dev", "1.2.3", true},
		{"1.2.3", "1.2.3-dev", false},
		{"not-a-version", "1.2.0", false},
		{"1.2.0", "not-a-version", false},
	}
	for _, c := range cases {
		if got := IsNewer(c.current, c.latest); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}
