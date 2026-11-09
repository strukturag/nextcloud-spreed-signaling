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
 */package client

import (
	"testing"
	"time"

	"github.com/dlintw/goconf"
	"github.com/stretchr/testify/assert"

	logtest "github.com/strukturag/nextcloud-spreed-signaling/v2/log/test"
)

func TestTimeoutsReadWait(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	assert.Equal(3*time.Second, Timeouts{
		PingInterval: time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 4 * time.Second,
	}.ReadWait())
	assert.Equal(DefaultPingInterval+DefaultReadTimeout, Timeouts{}.WithDefaults().ReadWait())
}

func TestTimeoutsWithDefaults(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	assert.Equal(Timeouts{
		PingInterval: DefaultPingInterval,
		ReadTimeout:  DefaultReadTimeout,
		WriteTimeout: DefaultWriteTimeout,
	}, Timeouts{}.WithDefaults())
	assert.Equal(Timeouts{
		PingInterval: time.Second,
		ReadTimeout:  DefaultReadTimeout,
		WriteTimeout: 2 * time.Second,
	}, Timeouts{
		PingInterval: time.Second,
		WriteTimeout: 2 * time.Second,
	}.WithDefaults())
}

func TestLoadTimeouts(t *testing.T) {
	t.Parallel()
	logger := logtest.NewLoggerForTest(t)

	testcases := []struct {
		value    string
		expected time.Duration
	}{
		{"", -1},
		{"30", 30 * time.Second},
		{"1", time.Second},
		{"0", -1},
		{"-5", -1},
		{"invalid", -1},
	}
	defaults := Timeouts{
		WriteTimeout: 10 * time.Second,
	}
	for _, section := range []string{"http", "https"} {
		for _, tc := range testcases {
			config := goconf.NewConfigFile()
			if tc.value != "" {
				config.AddOption(section, "pinginterval", tc.value)
				config.AddOption(section, "readtimeout", tc.value)
				config.AddOption(section, "writetimeout", tc.value)
			}

			expected := Timeouts{
				PingInterval: tc.expected,
				ReadTimeout:  tc.expected,
				WriteTimeout: tc.expected,
			}
			if tc.expected < 0 {
				expected = Timeouts{
					PingInterval: DefaultPingInterval,
					ReadTimeout:  DefaultReadTimeout,
					WriteTimeout: 10 * time.Second,
				}
			}
			assert.Equal(t, expected, LoadTimeouts(logger, config, section, defaults), "failed for %q in %s", tc.value, section)
		}
	}

	// Values are only read from the given section.
	config := goconf.NewConfigFile()
	config.AddOption("https", "pinginterval", "10")
	config.AddOption("https", "readtimeout", "11")
	config.AddOption("https", "writetimeout", "12")
	assert.Equal(t, Timeouts{
		PingInterval: DefaultPingInterval,
		ReadTimeout:  DefaultReadTimeout,
		WriteTimeout: 10 * time.Second,
	}, LoadTimeouts(logger, config, "http", defaults))
	assert.Equal(t, Timeouts{
		PingInterval: 10 * time.Second,
		ReadTimeout:  11 * time.Second,
		WriteTimeout: 12 * time.Second,
	}, LoadTimeouts(logger, config, "https", defaults))
}
