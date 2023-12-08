/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2023 struktur AG
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
	"errors"

	"github.com/dlintw/goconf"
)

func GetStringOptions(config *goconf.ConfigFile, section string, ignoreErrors bool) (map[string]string, error) {
	options, _ := config.GetOptions(section)
	if len(options) == 0 {
		return nil, nil
	}

	result := make(map[string]string)
	for _, option := range options {
		value, err := config.GetString(section, option)
		if err != nil {
			if ignoreErrors {
				continue
			}

			var ge goconf.GetError
			if errors.As(err, &ge) && ge.Reason == goconf.OptionNotFound {
				// Skip options from "default" section.
				continue
			}

			return nil, err
		}

		result[option] = value
	}

	return result, nil
}
