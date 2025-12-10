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
package talk

import (
	"encoding/json"
	"net/url"
	"strings"
)

type OcsMeta struct {
	Status     string `json:"status"`
	StatusCode int    `json:"statuscode"`
	Message    string `json:"message"`
}

type OcsBody struct {
	Meta OcsMeta         `json:"meta"`
	Data json.RawMessage `json:"data"`
}

type OcsResponse struct {
	json.Marshaler
	json.Unmarshaler

	Ocs *OcsBody `json:"ocs"`
}

func IsOcsRequest(u *url.URL) bool {
	return strings.Contains(u.Path, "/ocs/v2.php") || strings.Contains(u.Path, "/ocs/v1.php")
}
