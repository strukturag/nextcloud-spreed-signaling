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
package mock

import (
	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/geoip"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
)

type Listener struct {
	publicId api.PublicSessionId
}

func NewListener(publicId api.PublicSessionId) *Listener {
	return &Listener{
		publicId: publicId,
	}
}

func (m *Listener) PublicId() api.PublicSessionId {
	return m.publicId
}

func (m *Listener) OnUpdateOffer(client sfu.Client, offer api.StringMap) {

}

func (m *Listener) OnIceCandidate(client sfu.Client, candidate any) {

}

func (m *Listener) OnIceCompleted(client sfu.Client) {

}

func (m *Listener) SubscriberSidUpdated(subscriber sfu.Subscriber) {

}

func (m *Listener) PublisherClosed(publisher sfu.Publisher) {

}

func (m *Listener) SubscriberClosed(subscriber sfu.Subscriber) {

}

type Initiator struct {
	country geoip.Country
}

func NewInitiator(country geoip.Country) *Initiator {
	return &Initiator{
		country: country,
	}
}

func (m *Initiator) Country() geoip.Country {
	return m.country
}
