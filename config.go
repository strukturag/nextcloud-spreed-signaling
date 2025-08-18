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
	"iter"
	"os"
	"regexp"
	"strings"

	"github.com/dlintw/goconf"
)

var (
	searchVarsRegexp = regexp.MustCompile(`\$\([A-Za-z][A-Za-z0-9_]*\)`)
)

func replaceEnvVars(s string) string {
	return searchVarsRegexp.ReplaceAllStringFunc(s, func(name string) string {
		name = name[2 : len(name)-1]
		value, found := os.LookupEnv(name)
		if !found {
			return name
		}

		return value
	})
}

// GetStringOptionWithEnv will get the string option and resolve any environment
// variable references in the form "$(VAR)".
func GetStringOptionWithEnv(config *goconf.ConfigFile, section string, option string) (string, error) {
	value, err := config.GetString(section, option)
	if err != nil {
		return "", err
	}

	value = replaceEnvVars(value)
	return value, nil
}

func GetStringOptions(config *goconf.ConfigFile, section string, ignoreErrors bool) (map[string]string, error) {
	options, _ := config.GetOptions(section)
	if len(options) == 0 {
		return nil, nil
	}

	result := make(map[string]string)
	for _, option := range options {
		value, err := GetStringOptionWithEnv(config, section, option)
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

// SplitEntries returns an iterator over all non-empty substrings of s separated
// by sep.
func SplitEntries(s string, sep string) iter.Seq[string] {
	return func(yield func(entry string) bool) {
		for entry := range strings.SplitSeq(s, sep) {
			if entry = strings.TrimSpace(entry); entry != "" {
				if !yield(entry) {
					return
				}
			}
		}
	}
}
