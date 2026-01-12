/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2026 struktur AG
 *
 * @author Joachim Bauch <bauch@struktur.de>
 *
 * @license GNU AGPL version 3 or any later version
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */
package internal

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMockLookup(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)

	host1 := "domain1.invalid"
	host2 := "domain2.invalid"

	lookup := NewMockLookup()
	assert.Empty(lookup.Get(host1))
	assert.Empty(lookup.Get(host2))

	ips := []net.IP{
		net.ParseIP("1.2.3.4"),
	}
	lookup.Set(host1, ips)
	assert.Equal(ips, lookup.Get(host1))
	assert.Empty(lookup.Get(host2))

	if resolved, err := lookup.Lookup(host1); assert.NoError(err) {
		assert.Equal(ips, resolved)
	}
	var de *net.DNSError
	if resolved, err := lookup.Lookup(host2); assert.ErrorAs(err, &de, "expected error, got %+v", resolved) {
		assert.True(de.IsNotFound)
		assert.Equal(host2, de.Name)
	}
}
