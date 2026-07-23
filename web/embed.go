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

// Package web embeds the HTML templates into the compiled binary so the
// app doesn't depend on a working-directory-relative path at runtime.
//
// The "all:" prefix is required: Go's go:embed silently EXCLUDES any
// file/dir starting with "_" or "." unless the pattern has that prefix -
// every fragment template here is intentionally named with a leading
// underscore (a common "partial, not a full page" convention), so without
// "all:" they'd be silently dropped from the embedded FS.
package web

import "embed"

//go:embed all:templates
var TemplatesFS embed.FS
