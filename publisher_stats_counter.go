/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2021 struktur AG
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
	"sync"
)

type publisherStatsCounter struct {
	mu sync.Mutex

	// +checklocks:mu
	streamTypes map[StreamType]bool
	// +checklocks:mu
	subscribers map[string]bool
}

func (c *publisherStatsCounter) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	count := len(c.subscribers)
	for streamType := range c.streamTypes {
		statsMcuPublisherStreamTypesCurrent.WithLabelValues(string(streamType)).Dec()
		statsMcuSubscriberStreamTypesCurrent.WithLabelValues(string(streamType)).Sub(float64(count))
	}
	c.streamTypes = nil
	c.subscribers = nil
}

func (c *publisherStatsCounter) EnableStream(streamType StreamType, enable bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if enable == c.streamTypes[streamType] {
		return
	}

	if enable {
		if c.streamTypes == nil {
			c.streamTypes = make(map[StreamType]bool)
		}
		c.streamTypes[streamType] = true
		statsMcuPublisherStreamTypesCurrent.WithLabelValues(string(streamType)).Inc()
		statsMcuSubscriberStreamTypesCurrent.WithLabelValues(string(streamType)).Add(float64(len(c.subscribers)))
	} else {
		delete(c.streamTypes, streamType)
		statsMcuPublisherStreamTypesCurrent.WithLabelValues(string(streamType)).Dec()
		statsMcuSubscriberStreamTypesCurrent.WithLabelValues(string(streamType)).Sub(float64(len(c.subscribers)))
	}
}

func (c *publisherStatsCounter) AddSubscriber(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.subscribers[id] {
		return
	}

	if c.subscribers == nil {
		c.subscribers = make(map[string]bool)
	}
	c.subscribers[id] = true
	for streamType := range c.streamTypes {
		statsMcuSubscriberStreamTypesCurrent.WithLabelValues(string(streamType)).Inc()
	}
}

func (c *publisherStatsCounter) RemoveSubscriber(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.subscribers[id] {
		return
	}

	delete(c.subscribers, id)
	for streamType := range c.streamTypes {
		statsMcuSubscriberStreamTypesCurrent.WithLabelValues(string(streamType)).Dec()
	}
}
