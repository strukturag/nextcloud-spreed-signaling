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
package janus

import (
	"fmt"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
)

type streamSelection struct {
	substream *int
	temporal  *int
	audio     *bool
	video     *bool
}

func (s *streamSelection) HasValues() bool {
	return s.substream != nil || s.temporal != nil || s.audio != nil || s.video != nil
}

func (s *streamSelection) AddToMessage(message api.StringMap) {
	if s.substream != nil {
		message["substream"] = *s.substream
	}
	if s.temporal != nil {
		message["temporal"] = *s.temporal
	}
	if s.audio != nil {
		message["audio"] = *s.audio
	}
	if s.video != nil {
		message["video"] = *s.video
	}
}

func parseStreamSelection(payload api.StringMap) (*streamSelection, error) {
	var stream streamSelection
	if value, found := payload["substream"]; found {
		switch value := value.(type) {
		case int:
			stream.substream = &value
		case float32:
			stream.substream = internal.MakePtr(int(value))
		case float64:
			stream.substream = internal.MakePtr(int(value))
		default:
			return nil, fmt.Errorf("unsupported substream value: %v", value)
		}
	}

	if value, found := payload["temporal"]; found {
		switch value := value.(type) {
		case int:
			stream.temporal = &value
		case float32:
			stream.temporal = internal.MakePtr(int(value))
		case float64:
			stream.temporal = internal.MakePtr(int(value))
		default:
			return nil, fmt.Errorf("unsupported temporal value: %v", value)
		}
	}

	if value, found := payload["audio"]; found {
		switch value := value.(type) {
		case bool:
			stream.audio = &value
		default:
			return nil, fmt.Errorf("unsupported audio value: %v", value)
		}
	}

	if value, found := payload["video"]; found {
		switch value := value.(type) {
		case bool:
			stream.video = &value
		default:
			return nil, fmt.Errorf("unsupported video value: %v", value)
		}
	}

	return &stream, nil
}
