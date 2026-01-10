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
package janus

import (
	"sync"

	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
)

type publisherStatsCounterStats interface {
	IncPublisherStream(streamType sfu.StreamType)
	DecPublisherStream(streamType sfu.StreamType)
	IncSubscriberStream(streamType sfu.StreamType)
	DecSubscriberStream(streamType sfu.StreamType)
	AddSubscriberStreams(streamType sfu.StreamType, count int)
	SubSubscriberStreams(streamType sfu.StreamType, count int)
}

type prometheusPublisherStats struct{}

func (s *prometheusPublisherStats) IncPublisherStream(streamType sfu.StreamType) {
	statsMcuPublisherStreamTypesCurrent.WithLabelValues(string(streamType)).Inc()
}

func (s *prometheusPublisherStats) DecPublisherStream(streamType sfu.StreamType) {
	statsMcuPublisherStreamTypesCurrent.WithLabelValues(string(streamType)).Dec()
}

func (s *prometheusPublisherStats) IncSubscriberStream(streamType sfu.StreamType) {
	statsMcuSubscriberStreamTypesCurrent.WithLabelValues(string(streamType)).Inc()
}

func (s *prometheusPublisherStats) DecSubscriberStream(streamType sfu.StreamType) {
	statsMcuSubscriberStreamTypesCurrent.WithLabelValues(string(streamType)).Dec()
}

func (s *prometheusPublisherStats) AddSubscriberStreams(streamType sfu.StreamType, count int) {
	statsMcuSubscriberStreamTypesCurrent.WithLabelValues(string(streamType)).Add(float64(count))
}

func (s *prometheusPublisherStats) SubSubscriberStreams(streamType sfu.StreamType, count int) {
	statsMcuSubscriberStreamTypesCurrent.WithLabelValues(string(streamType)).Sub(float64(count))
}

var (
	defaultPublisherStats = &prometheusPublisherStats{} // +checklocksignore: Global readonly variable.
)

type publisherStatsCounter struct {
	mu sync.Mutex

	// +checklocks:mu
	streamTypes map[sfu.StreamType]bool
	// +checklocks:mu
	subscribers map[string]bool
	// +checklocks:mu
	stats publisherStatsCounterStats
}

func (c *publisherStatsCounter) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	stats := c.stats
	if stats == nil {
		stats = defaultPublisherStats
	}

	count := len(c.subscribers)
	for streamType := range c.streamTypes {
		stats.DecPublisherStream(streamType)
		stats.SubSubscriberStreams(streamType, count)
	}
	c.streamTypes = nil
	c.subscribers = nil
}

func (c *publisherStatsCounter) EnableStream(streamType sfu.StreamType, enable bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if enable == c.streamTypes[streamType] {
		return
	}

	stats := c.stats
	if stats == nil {
		stats = defaultPublisherStats
	}

	if enable {
		if c.streamTypes == nil {
			c.streamTypes = make(map[sfu.StreamType]bool)
		}
		c.streamTypes[streamType] = true
		stats.IncPublisherStream(streamType)
		stats.AddSubscriberStreams(streamType, len(c.subscribers))
	} else {
		delete(c.streamTypes, streamType)
		stats.DecPublisherStream(streamType)
		stats.SubSubscriberStreams(streamType, len(c.subscribers))
	}
}

func (c *publisherStatsCounter) AddSubscriber(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.subscribers[id] {
		return
	}

	stats := c.stats
	if stats == nil {
		stats = defaultPublisherStats
	}

	if c.subscribers == nil {
		c.subscribers = make(map[string]bool)
	}
	c.subscribers[id] = true
	for streamType := range c.streamTypes {
		stats.IncSubscriberStream(streamType)
	}
}

func (c *publisherStatsCounter) RemoveSubscriber(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.subscribers[id] {
		return
	}

	stats := c.stats
	if stats == nil {
		stats = defaultPublisherStats
	}

	delete(c.subscribers, id)
	for streamType := range c.streamTypes {
		stats.DecSubscriberStream(streamType)
	}
}
