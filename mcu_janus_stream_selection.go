/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2017 struktur AG
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
	"database/sql"
	"fmt"
)

type streamSelection struct {
	substream sql.NullInt16
	temporal  sql.NullInt16
	audio     sql.NullBool
	video     sql.NullBool
}

func (s *streamSelection) HasValues() bool {
	return s.substream.Valid || s.temporal.Valid || s.audio.Valid || s.video.Valid
}

func (s *streamSelection) AddToMessage(message map[string]any) {
	if s.substream.Valid {
		message["substream"] = s.substream.Int16
	}
	if s.temporal.Valid {
		message["temporal"] = s.temporal.Int16
	}
	if s.audio.Valid {
		message["audio"] = s.audio.Bool
	}
	if s.video.Valid {
		message["video"] = s.video.Bool
	}
}

func parseStreamSelection(payload map[string]any) (*streamSelection, error) {
	var stream streamSelection
	if value, found := payload["substream"]; found {
		switch value := value.(type) {
		case int:
			stream.substream.Valid = true
			stream.substream.Int16 = int16(value)
		case float32:
			stream.substream.Valid = true
			stream.substream.Int16 = int16(value)
		case float64:
			stream.substream.Valid = true
			stream.substream.Int16 = int16(value)
		default:
			return nil, fmt.Errorf("unsupported substream value: %v", value)
		}
	}

	if value, found := payload["temporal"]; found {
		switch value := value.(type) {
		case int:
			stream.temporal.Valid = true
			stream.temporal.Int16 = int16(value)
		case float32:
			stream.temporal.Valid = true
			stream.temporal.Int16 = int16(value)
		case float64:
			stream.temporal.Valid = true
			stream.temporal.Int16 = int16(value)
		default:
			return nil, fmt.Errorf("unsupported temporal value: %v", value)
		}
	}

	if value, found := payload["audio"]; found {
		switch value := value.(type) {
		case bool:
			stream.audio.Valid = true
			stream.audio.Bool = value
		default:
			return nil, fmt.Errorf("unsupported audio value: %v", value)
		}
	}

	if value, found := payload["video"]; found {
		switch value := value.(type) {
		case bool:
			stream.video.Valid = true
			stream.video.Bool = value
		default:
			return nil, fmt.Errorf("unsupported video value: %v", value)
		}
	}

	return &stream, nil
}
