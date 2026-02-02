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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
)

type mockPublisherStats struct {
	publishers  map[sfu.StreamType]int
	subscribers map[sfu.StreamType]int
}

func (s *mockPublisherStats) IncPublisherStream(streamType sfu.StreamType) {
	if s.publishers == nil {
		s.publishers = make(map[sfu.StreamType]int)
	}
	s.publishers[streamType]++
}

func (s *mockPublisherStats) DecPublisherStream(streamType sfu.StreamType) {
	if s.publishers == nil {
		s.publishers = make(map[sfu.StreamType]int)
	}
	s.publishers[streamType]--
}

func (s *mockPublisherStats) IncSubscriberStream(streamType sfu.StreamType) {
	if s.subscribers == nil {
		s.subscribers = make(map[sfu.StreamType]int)
	}
	s.subscribers[streamType]++
}

func (s *mockPublisherStats) DecSubscriberStream(streamType sfu.StreamType) {
	if s.subscribers == nil {
		s.subscribers = make(map[sfu.StreamType]int)
	}
	s.subscribers[streamType]--
}

func (s *mockPublisherStats) AddSubscriberStreams(streamType sfu.StreamType, count int) {
	if s.subscribers == nil {
		s.subscribers = make(map[sfu.StreamType]int)
	}
	s.subscribers[streamType] += count
}

func (s *mockPublisherStats) SubSubscriberStreams(streamType sfu.StreamType, count int) {
	if s.subscribers == nil {
		s.subscribers = make(map[sfu.StreamType]int)
	}
	s.subscribers[streamType] -= count
}

func (s *mockPublisherStats) Publishers(streamType sfu.StreamType) int {
	return s.publishers[streamType]
}

func (s *mockPublisherStats) Subscribers(streamType sfu.StreamType) int {
	return s.subscribers[streamType]
}

func TestPublisherStatsPrometheus(t *testing.T) {
	t.Parallel()

	RegisterStats()
	UnregisterStats()
}

func TestPublisherStatsCounter(t *testing.T) {
	t.Parallel()
	assert := assert.New(t)

	stats := &mockPublisherStats{}
	c := publisherStatsCounter{
		stats: stats,
	}

	c.Reset()
	assert.Equal(0, stats.Publishers("audio"))
	c.EnableStream("audio", false)
	assert.Equal(0, stats.Publishers("audio"))
	c.EnableStream("audio", true)
	assert.Equal(1, stats.Publishers("audio"))
	c.EnableStream("audio", true)
	assert.Equal(1, stats.Publishers("audio"))
	c.EnableStream("video", true)
	assert.Equal(1, stats.Publishers("audio"))
	assert.Equal(1, stats.Publishers("video"))
	c.EnableStream("audio", false)
	assert.Equal(0, stats.Publishers("audio"))
	assert.Equal(1, stats.Publishers("video"))
	c.EnableStream("audio", false)
	assert.Equal(0, stats.Publishers("audio"))
	assert.Equal(1, stats.Publishers("video"))

	c.AddSubscriber("1")
	assert.Equal(0, stats.Publishers("audio"))
	assert.Equal(1, stats.Publishers("video"))
	assert.Equal(0, stats.Subscribers("audio"))
	assert.Equal(1, stats.Subscribers("video"))
	c.EnableStream("audio", true)
	assert.Equal(1, stats.Publishers("audio"))
	assert.Equal(1, stats.Publishers("video"))
	assert.Equal(1, stats.Subscribers("audio"))
	assert.Equal(1, stats.Subscribers("video"))
	c.AddSubscriber("1")
	assert.Equal(1, stats.Publishers("audio"))
	assert.Equal(1, stats.Publishers("video"))
	assert.Equal(1, stats.Subscribers("audio"))
	assert.Equal(1, stats.Subscribers("video"))

	c.AddSubscriber("2")
	assert.Equal(1, stats.Publishers("audio"))
	assert.Equal(1, stats.Publishers("video"))
	assert.Equal(2, stats.Subscribers("audio"))
	assert.Equal(2, stats.Subscribers("video"))

	c.RemoveSubscriber("3")
	assert.Equal(1, stats.Publishers("audio"))
	assert.Equal(1, stats.Publishers("video"))
	assert.Equal(2, stats.Subscribers("audio"))
	assert.Equal(2, stats.Subscribers("video"))

	c.RemoveSubscriber("1")
	assert.Equal(1, stats.Publishers("audio"))
	assert.Equal(1, stats.Publishers("video"))
	assert.Equal(1, stats.Subscribers("audio"))
	assert.Equal(1, stats.Subscribers("video"))

	c.AddSubscriber("1")
	assert.Equal(1, stats.Publishers("audio"))
	assert.Equal(1, stats.Publishers("video"))
	assert.Equal(2, stats.Subscribers("audio"))
	assert.Equal(2, stats.Subscribers("video"))

	c.EnableStream("audio", false)
	assert.Equal(0, stats.Publishers("audio"))
	assert.Equal(1, stats.Publishers("video"))
	assert.Equal(0, stats.Subscribers("audio"))
	assert.Equal(2, stats.Subscribers("video"))

	c.EnableStream("audio", true)
	assert.Equal(1, stats.Publishers("audio"))
	assert.Equal(1, stats.Publishers("video"))
	assert.Equal(2, stats.Subscribers("audio"))
	assert.Equal(2, stats.Subscribers("video"))

	c.EnableStream("audio", false)
	c.EnableStream("video", false)
	assert.Equal(0, stats.Publishers("audio"))
	assert.Equal(0, stats.Publishers("video"))
	assert.Equal(0, stats.Subscribers("audio"))
	assert.Equal(0, stats.Subscribers("video"))
}
