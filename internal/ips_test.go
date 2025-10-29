/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2025 struktur AG
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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsLoopbackIP(t *testing.T) {
	loopback := []string{
		"127.0.0.1",
		"127.1.0.1",
		"::1",
		"::ffff:127.0.0.1",
	}
	nonLoopback := []string{
		"",
		"invalid",
		"1.2.3.4",
		"::0",
		"::2",
	}
	assert := assert.New(t)
	for _, ip := range loopback {
		assert.True(IsLoopbackIP(ip), "should be loopback: %s", ip)
	}
	for _, ip := range nonLoopback {
		assert.False(IsLoopbackIP(ip), "should not be loopback: %s", ip)
	}
}

func TestIsPrivateIP(t *testing.T) {
	private := []string{
		"10.1.2.3",
		"172.20.21.22",
		"192.168.10.20",
		"fdea:aef9:06e3:bb24:1234:1234:1234:1234",
		"fd12:3456:789a:1::1",
	}
	nonPrivate := []string{
		"",
		"invalid",
		"127.0.0.1",
		"1.2.3.4",
		"::0",
		"::1",
		"::2",
		"1234:3456:789a:1::1",
	}
	assert := assert.New(t)
	for _, ip := range private {
		assert.True(IsPrivateIP(ip), "should be private: %s", ip)
	}
	for _, ip := range nonPrivate {
		assert.False(IsPrivateIP(ip), "should not be private: %s", ip)
	}
}
