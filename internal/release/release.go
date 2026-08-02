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

// Package release checks GitHub for a newer published version of this
// plugin than the one currently running, so it can report the result via
// GET /version (see internal/handlers/version.go) - hhq itself never talks
// to GitHub on this plugin's behalf, since the plugin is the one that knows
// its own repo. Keyless (unauthenticated), matching hhq's own "no new
// secrets to manage" approach.
package release

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Repo is this plugin's own GitHub repo, hardcoded here rather than made
// configurable - the plugin author is the one who knows where their plugin
// lives, an operator deploying it has no reason to need to override this.
var Repo = "mscreations/billtracker-plugin"

// GitHubAPIBase is a var (not const) so tests can point it at a local
// httptest server instead of the real GitHub API, the same pattern hhq's
// own internal/release uses.
var GitHubAPIBase = "https://api.github.com"

// Info is the result of CheckForUpdate - the latest known version and, best
// effort, a short changelog describing it.
type Info struct {
	Version   string
	Changelog string
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Body    string `json:"body"`
}

type githubTag struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

type githubCommit struct {
	Commit struct {
		Message string `json:"message"`
	} `json:"commit"`
}

// CheckForUpdate checks repo's GitHub repo for a version newer than
// currentVersion. A "-dev" currentVersion (this plugin's dev-branch builds,
// same scheme as hhq's own) checks the tags API instead of releases/latest -
// dev builds only ever get a git tag pushed on every commit, never a GitHub
// Release (only a promotion to main cuts one of those), so releases/latest
// would never reflect a dev build's actual newest tag.
func CheckForUpdate(ctx context.Context, repo, currentVersion string) (*Info, error) {
	if strings.HasSuffix(currentVersion, "-dev") {
		return checkDevTags(ctx, repo)
	}
	return checkLatestRelease(ctx, repo)
}

// checkLatestRelease calls GitHub's "latest release" endpoint, which only
// ever returns non-prerelease, non-draft releases - this plugin's versioning
// workflow only cuts a GitHub Release on main-branch (non "-dev") tags, so
// this naturally means "the latest promoted version". The release's own
// body (or, if blank, its name) is used as the changelog.
func checkLatestRelease(ctx context.Context, repo string) (*Info, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", GitHubAPIBase, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("release: requesting latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release: latest release request returned %s", resp.Status)
	}

	var parsed githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("release: decoding latest release response: %w", err)
	}

	changelog := parsed.Body
	if changelog == "" {
		changelog = parsed.Name
	}
	return &Info{
		Version:   strings.TrimPrefix(parsed.TagName, "v"),
		Changelog: changelog,
	}, nil
}

// checkDevTags paginates GitHub's tags API to find the highest-versioned
// "-dev" tag, then best-effort fetches that tag's commit message (first
// line) as the changelog - dev tags have no GitHub Release object to source
// real release notes from, so the commit subject is the closest available
// substitute. A failure fetching the commit message is non-fatal (empty
// changelog rather than erroring the whole check).
func checkDevTags(ctx context.Context, repo string) (*Info, error) {
	const perPage = 100
	const maxTagPages = 10

	var best *semver
	var bestTag githubTag

	for page := 1; page <= maxTagPages; page++ {
		url := fmt.Sprintf("%s/repos/%s/tags?per_page=%d&page=%d", GitHubAPIBase, repo, perPage, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("release: requesting tags (page %d): %w", page, err)
		}

		var tags []githubTag
		decodeErr := json.NewDecoder(resp.Body).Decode(&tags)
		status := resp.Status
		statusCode := resp.StatusCode
		resp.Body.Close()

		if statusCode != http.StatusOK {
			return nil, fmt.Errorf("release: tags request returned %s", status)
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("release: decoding tags response: %w", decodeErr)
		}

		for _, t := range tags {
			v, ok := parseVersion(strings.TrimPrefix(t.Name, "v"))
			if !ok || !v.dev {
				continue
			}
			if best == nil || compareVersions(v, *best) > 0 {
				vCopy := v
				best = &vCopy
				bestTag = t
			}
		}

		if len(tags) < perPage {
			break
		}
	}

	if best == nil {
		return nil, fmt.Errorf("release: no dev-versioned tags found")
	}

	info := &Info{Version: strings.TrimPrefix(bestTag.Name, "v")}
	if bestTag.Commit.SHA != "" {
		info.Changelog = commitSubject(ctx, repo, bestTag.Commit.SHA)
	}
	return info, nil
}

// commitSubject best-effort fetches sha's commit message and returns its
// first line - a blank string on any failure, never an error, since a
// missing changelog shouldn't fail the whole version check.
func commitSubject(ctx context.Context, repo, sha string) string {
	url := fmt.Sprintf("%s/repos/%s/commits/%s", GitHubAPIBase, repo, sha)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var c githubCommit
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil {
		return ""
	}
	firstLine, _, _ := strings.Cut(c.Commit.Message, "\n")
	return firstLine
}

// IsNewer reports whether latest is a newer version than current, per this
// plugin's version scheme (MAJOR.MINOR.PATCH, optionally suffixed "-dev" for
// dev-branch builds - a "-dev" build is always considered older than the
// release it was built against, e.g. "1.2.3-dev" < "1.2.3"). Malformed
// version strings are treated as "not newer" rather than erroring, since this
// only ever drives a cosmetic /version field.
func IsNewer(current, latest string) bool {
	c, ok := parseVersion(current)
	if !ok {
		return false
	}
	l, ok := parseVersion(latest)
	if !ok {
		return false
	}
	return compareVersions(l, c) > 0
}

type semver struct {
	major, minor, patch int
	dev                 bool
}

func parseVersion(v string) (semver, bool) {
	var s semver
	if strings.HasSuffix(v, "-dev") {
		s.dev = true
		v = strings.TrimSuffix(v, "-dev")
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return semver{}, false
		}
		nums[i] = n
	}
	s.major, s.minor, s.patch = nums[0], nums[1], nums[2]
	return s, true
}

// compareVersions returns -1/0/1 as a < b / a == b / a > b, treating a "-dev"
// build as older than the same non-dev version (e.g. 1.2.3-dev < 1.2.3).
func compareVersions(a, b semver) int {
	if a.major != b.major {
		return cmpInt(a.major, b.major)
	}
	if a.minor != b.minor {
		return cmpInt(a.minor, b.minor)
	}
	if a.patch != b.patch {
		return cmpInt(a.patch, b.patch)
	}
	if a.dev != b.dev {
		if a.dev {
			return -1
		}
		return 1
	}
	return 0
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
