/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2024 struktur AG
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
package signaling

import (
	"testing"
)

func TestGetFmtpValueH264(t *testing.T) {
	testcases := []struct {
		fmtp    string
		profile string
	}{
		{
			"",
			"",
		},
		{
			"level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42001f",
			"42001f",
		},
		{
			"level-asymmetry-allowed=1;packetization-mode=0",
			"",
		},
		{
			"level-asymmetry-allowed=1; packetization-mode=0; profile-level-id = 42001f",
			"42001f",
		},
	}

	for _, tc := range testcases {
		value, found := getFmtpValue(tc.fmtp, "profile-level-id")
		if !found && tc.profile != "" {
			t.Errorf("did not find profile \"%s\" in \"%s\"", tc.profile, tc.fmtp)
		} else if found && tc.profile == "" {
			t.Errorf("did not expect profile in \"%s\" but got \"%s\"", tc.fmtp, value)
		} else if found && tc.profile != value {
			t.Errorf("expected profile \"%s\" in \"%s\" but got \"%s\"", tc.profile, tc.fmtp, value)
		}
	}
}

func TestGetFmtpValueVP9(t *testing.T) {
	testcases := []struct {
		fmtp    string
		profile string
	}{
		{
			"",
			"",
		},
		{
			"profile-id=0",
			"0",
		},
		{
			"profile-id = 0",
			"0",
		},
	}

	for _, tc := range testcases {
		value, found := getFmtpValue(tc.fmtp, "profile-id")
		if !found && tc.profile != "" {
			t.Errorf("did not find profile \"%s\" in \"%s\"", tc.profile, tc.fmtp)
		} else if found && tc.profile == "" {
			t.Errorf("did not expect profile in \"%s\" but got \"%s\"", tc.fmtp, value)
		} else if found && tc.profile != value {
			t.Errorf("expected profile \"%s\" in \"%s\" but got \"%s\"", tc.profile, tc.fmtp, value)
		}
	}
}
