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

import "sync"

// Cache holds the most recently checked Info in memory, refreshed
// periodically by the scheduler (see internal/scheduler's runVersionCheck).
// No persisted table - this plugin only ever checks its own single repo, so
// unlike hhq's map-keyed cache for multiple plugins, one value is enough.
type Cache struct {
	mu   sync.RWMutex
	info *Info
}

// Get returns the cached info, or (nil, false) if nothing has been checked
// yet (e.g. the app just started, or GitHub was unreachable).
func (c *Cache) Get() (*Info, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.info == nil {
		return nil, false
	}
	return c.info, true
}

// Set stores the latest checked info.
func (c *Cache) Set(i *Info) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.info = i
}
