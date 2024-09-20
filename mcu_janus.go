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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"github.com/notedit/janus-go"
	"go.uber.org/zap"
)

const (
	pluginVideoRoom = "janus.plugin.videoroom"

	keepaliveInterval = 30 * time.Second

	videoPublisherUserId  = 1
	screenPublisherUserId = 2

	initialReconnectInterval = 1 * time.Second
	maxReconnectInterval     = 32 * time.Second

	defaultMaxStreamBitrate = 1024 * 1024
	defaultMaxScreenBitrate = 2048 * 1024
)

var (
	ErrRemoteStreamsNotSupported = errors.New("Need Janus 1.1.0 for remote streams")

	streamTypeUserIds = map[StreamType]uint64{
		StreamTypeVideo:  videoPublisherUserId,
		StreamTypeScreen: screenPublisherUserId,
	}
)

func getStreamId(publisherId string, streamType StreamType) string {
	return fmt.Sprintf("%s|%s", publisherId, streamType)
}

func getPluginValue(data janus.PluginData, pluginName string, key string) interface{} {
	if data.Plugin != pluginName {
		return nil
	}

	return data.Data[key]
}

func convertIntValue(value interface{}) (uint64, error) {
	switch t := value.(type) {
	case float64:
		if t < 0 {
			return 0, fmt.Errorf("Unsupported float64 number: %+v", t)
		}
		return uint64(t), nil
	case uint64:
		return t, nil
	case int64:
		if t < 0 {
			return 0, fmt.Errorf("Unsupported int64 number: %+v", t)
		}
		return uint64(t), nil
	case json.Number:
		r, err := t.Int64()
		if err != nil {
			return 0, err
		} else if r < 0 {
			return 0, fmt.Errorf("Unsupported JSON number: %+v", t)
		}
		return uint64(r), nil
	default:
		return 0, fmt.Errorf("Unknown number type: %+v", t)
	}
}

func getPluginIntValue(data janus.PluginData, pluginName string, key string) uint64 {
	val := getPluginValue(data, pluginName, key)
	if val == nil {
		return 0
	}

	result, err := convertIntValue(val)
	if err != nil {
		zap.L().Warn("Invalid integer value",
			zap.String("key", key),
			zap.Any("value", val),
			zap.Error(err),
		)
		result = 0
	}
	return result
}

func getPluginStringValue(data janus.PluginData, pluginName string, key string) string {
	val := getPluginValue(data, pluginName, key)
	if val == nil {
		return ""
	}

	strVal, ok := val.(string)
	if !ok {
		return ""
	}

	return strVal
}

// TODO(jojo): Lots of error handling still missing.

type clientInterface interface {
	NotifyReconnected()
}

type mcuJanus struct {
	log *zap.Logger
	url string
	mu  sync.Mutex

	maxStreamBitrate atomic.Int32
	maxScreenBitrate atomic.Int32
	mcuTimeout       time.Duration

	gw      *JanusGateway
	session *JanusSession
	handle  *JanusHandle

	version int

	closeChan chan struct{}

	muClients sync.Mutex
	clients   map[clientInterface]bool
	clientId  atomic.Uint64

	publishers         map[string]*mcuJanusPublisher
	publisherCreated   Notifier
	publisherConnected Notifier
	remotePublishers   map[string]*mcuJanusRemotePublisher

	reconnectTimer    *time.Timer
	reconnectInterval time.Duration

	connectedSince time.Time
	onConnected    atomic.Value
	onDisconnected atomic.Value
}

func emptyOnConnected()    {}
func emptyOnDisconnected() {}

func NewMcuJanus(ctx context.Context, log *zap.Logger, url string, config *goconf.ConfigFile) (Mcu, error) {
	maxStreamBitrate, _ := config.GetInt("mcu", "maxstreambitrate")
	if maxStreamBitrate <= 0 {
		maxStreamBitrate = defaultMaxStreamBitrate
	}
	maxScreenBitrate, _ := config.GetInt("mcu", "maxscreenbitrate")
	if maxScreenBitrate <= 0 {
		maxScreenBitrate = defaultMaxScreenBitrate
	}
	mcuTimeoutSeconds, _ := config.GetInt("mcu", "timeout")
	if mcuTimeoutSeconds <= 0 {
		mcuTimeoutSeconds = defaultMcuTimeoutSeconds
	}
	mcuTimeout := time.Duration(mcuTimeoutSeconds) * time.Second

	mcu := &mcuJanus{
		log:        log,
		url:        url,
		mcuTimeout: mcuTimeout,
		closeChan:  make(chan struct{}, 1),
		clients:    make(map[clientInterface]bool),

		publishers:       make(map[string]*mcuJanusPublisher),
		remotePublishers: make(map[string]*mcuJanusRemotePublisher),

		reconnectInterval: initialReconnectInterval,
	}
	mcu.maxStreamBitrate.Store(int32(maxStreamBitrate))
	mcu.maxScreenBitrate.Store(int32(maxScreenBitrate))
	mcu.onConnected.Store(emptyOnConnected)
	mcu.onDisconnected.Store(emptyOnDisconnected)

	mcu.reconnectTimer = time.AfterFunc(mcu.reconnectInterval, func() {
		mcu.doReconnect(context.Background())
	})
	mcu.reconnectTimer.Stop()
	if err := mcu.reconnect(ctx); err != nil {
		return nil, err
	}
	return mcu, nil
}

func (m *mcuJanus) disconnect() {
	if handle := m.handle; handle != nil {
		m.handle = nil
		m.closeChan <- struct{}{}
		if _, err := handle.Detach(context.TODO()); err != nil {
			m.log.Error("Error detaching Janus handle",
				zap.Uint64("handle", handle.Id),
				zap.Error(err),
			)
		}
	}
	if m.session != nil {
		if _, err := m.session.Destroy(context.TODO()); err != nil {
			m.log.Error("Error destroying Janus session",
				zap.Uint64("sessionid", m.session.Id),
				zap.Error(err),
			)
		}
		m.session = nil
	}
	if m.gw != nil {
		if err := m.gw.Close(); err != nil {
			m.log.Error("Error while closing connection to Janus",
				zap.Error(err),
			)
		}
		m.gw = nil
	}
}

func (m *mcuJanus) reconnect(ctx context.Context) error {
	m.disconnect()
	gw, err := NewJanusGateway(ctx, m.log, m.url, m)
	if err != nil {
		return err
	}

	m.gw = gw
	m.reconnectTimer.Stop()
	return nil
}

func (m *mcuJanus) doReconnect(ctx context.Context) {
	if err := m.reconnect(ctx); err != nil {
		m.scheduleReconnect(err)
		return
	}
	if err := m.Start(ctx); err != nil {
		m.scheduleReconnect(err)
		return
	}

	m.log.Info("Reconnection to Janus gateway successful")
	m.mu.Lock()
	clear(m.publishers)
	m.publisherCreated.Reset()
	m.publisherConnected.Reset()
	m.reconnectInterval = initialReconnectInterval
	m.mu.Unlock()

	m.muClients.Lock()
	for client := range m.clients {
		go client.NotifyReconnected()
	}
	m.muClients.Unlock()
}

func (m *mcuJanus) scheduleReconnect(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconnectTimer.Reset(m.reconnectInterval)
	if err == nil {
		m.log.Warn("Connection to Janus gateway was interrupted, reconnecting",
			zap.Duration("wait", m.reconnectInterval),
		)
	} else {
		m.log.Error("Reconnect to Janus gateway failed, reconnecting",
			zap.Duration("wait", m.reconnectInterval),
			zap.Error(err),
		)
	}

	m.reconnectInterval = m.reconnectInterval * 2
	if m.reconnectInterval > maxReconnectInterval {
		m.reconnectInterval = maxReconnectInterval
	}
}

func (m *mcuJanus) ConnectionInterrupted() {
	m.scheduleReconnect(nil)
	m.notifyOnDisconnected()
}

func (m *mcuJanus) isMultistream() bool {
	return m.version >= 1000
}

func (m *mcuJanus) hasRemotePublisher() bool {
	return m.version >= 1100
}

func (m *mcuJanus) Start(ctx context.Context) error {
	info, err := m.gw.Info(ctx)
	if err != nil {
		return err
	}

	m.log.Info("Connected to Janus",
		zap.String("name", info.Name),
		zap.String("version", info.VersionString),
		zap.String("author", info.Author),
	)
	plugin, found := info.Plugins[pluginVideoRoom]
	if !found {
		return fmt.Errorf("Plugin %s is not supported", pluginVideoRoom)
	}

	m.version = info.Version

	m.log.Info("Found Janus plugin",
		zap.String("name", plugin.Name),
		zap.String("version", plugin.VersionString),
		zap.String("author", plugin.Author),
	)
	if !info.DataChannels {
		return fmt.Errorf("Data channels are not supported")
	}

	m.log.Info("Data channels are supported")
	if !info.FullTrickle {
		m.log.Warn("Full-Trickle is NOT enabled in Janus!")
	} else {
		m.log.Info("Full-Trickle is enabled")
	}
	m.log.Info("Maximum bandwidth per publishing stream",
		zap.Int32("bitrate", m.maxStreamBitrate.Load()),
	)
	m.log.Info("Maximum bandwidth per screensharing stream",
		zap.Int32("bitrate", m.maxScreenBitrate.Load()),
	)

	if m.session, err = m.gw.Create(ctx); err != nil {
		m.disconnect()
		return err
	}
	m.log.Info("Created Janus session",
		zap.Uint64("sessionid", m.session.Id),
	)
	m.connectedSince = time.Now()

	if m.handle, err = m.session.Attach(ctx, pluginVideoRoom); err != nil {
		m.disconnect()
		return err
	}
	m.log.Info("Created Janus handle",
		zap.Uint64("handle", m.handle.Id),
	)

	go m.run()

	m.notifyOnConnected()
	return nil
}

func (m *mcuJanus) registerClient(client clientInterface) {
	m.muClients.Lock()
	m.clients[client] = true
	m.muClients.Unlock()
}

func (m *mcuJanus) unregisterClient(client clientInterface) {
	m.muClients.Lock()
	delete(m.clients, client)
	m.muClients.Unlock()
}

func (m *mcuJanus) run() {
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

loop:
	for {
		select {
		case <-ticker.C:
			m.sendKeepalive(context.Background())
		case <-m.closeChan:
			break loop
		}
	}
}

func (m *mcuJanus) Stop() {
	m.disconnect()
	m.reconnectTimer.Stop()
}

func (m *mcuJanus) Reload(config *goconf.ConfigFile) {
	maxStreamBitrate, _ := config.GetInt("mcu", "maxstreambitrate")
	if maxStreamBitrate <= 0 {
		maxStreamBitrate = defaultMaxStreamBitrate
	}
	m.log.Info("Maximum bandwidth per publishing stream",
		zap.Int32("bitrate", m.maxStreamBitrate.Load()),
	)
	m.maxStreamBitrate.Store(int32(maxStreamBitrate))

	maxScreenBitrate, _ := config.GetInt("mcu", "maxscreenbitrate")
	if maxScreenBitrate <= 0 {
		maxScreenBitrate = defaultMaxScreenBitrate
	}
	m.log.Info("Maximum bandwidth per screensharing stream",
		zap.Int32("bitrate", m.maxScreenBitrate.Load()),
	)
	m.maxScreenBitrate.Store(int32(maxScreenBitrate))
}

func (m *mcuJanus) SetOnConnected(f func()) {
	if f == nil {
		f = emptyOnConnected
	}

	m.onConnected.Store(f)
}

func (m *mcuJanus) notifyOnConnected() {
	f := m.onConnected.Load().(func())
	f()
}

func (m *mcuJanus) SetOnDisconnected(f func()) {
	if f == nil {
		f = emptyOnDisconnected
	}

	m.onDisconnected.Store(f)
}

func (m *mcuJanus) notifyOnDisconnected() {
	f := m.onDisconnected.Load().(func())
	f()
}

type mcuJanusConnectionStats struct {
	Url        string     `json:"url"`
	Connected  bool       `json:"connected"`
	Publishers int64      `json:"publishers"`
	Clients    int64      `json:"clients"`
	Uptime     *time.Time `json:"uptime,omitempty"`
}

func (m *mcuJanus) GetStats() interface{} {
	result := mcuJanusConnectionStats{
		Url: m.url,
	}
	if m.session != nil {
		result.Connected = true
		result.Uptime = &m.connectedSince
	}
	m.mu.Lock()
	result.Publishers = int64(len(m.publishers))
	m.mu.Unlock()
	m.muClients.Lock()
	result.Clients = int64(len(m.clients))
	m.muClients.Unlock()
	return result
}

func (m *mcuJanus) sendKeepalive(ctx context.Context) {
	if _, err := m.session.KeepAlive(ctx); err != nil {
		m.log.Error("Could not send Janus keepalive request",
			zap.Error(err),
		)
		if e, ok := err.(*janus.ErrorMsg); ok {
			switch e.Err.Code {
			case JANUS_ERROR_SESSION_NOT_FOUND:
				m.scheduleReconnect(err)
			}
		}
	}
}

func (m *mcuJanus) SubscriberConnected(id string, publisher string, streamType StreamType) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p, found := m.publishers[getStreamId(publisher, streamType)]; found {
		p.stats.AddSubscriber(id)
	}
}

func (m *mcuJanus) SubscriberDisconnected(id string, publisher string, streamType StreamType) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p, found := m.publishers[getStreamId(publisher, streamType)]; found {
		p.stats.RemoveSubscriber(id)
	}
}

func (m *mcuJanus) createPublisherRoom(ctx context.Context, handle *JanusHandle, id string, streamType StreamType, bitrate int) (uint64, int, error) {
	create_msg := map[string]interface{}{
		"request":     "create",
		"description": getStreamId(id, streamType),
		// We publish every stream in its own Janus room.
		"publishers": 1,
		// Do not use the video-orientation RTP extension as it breaks video
		// orientation changes in Firefox.
		"videoorient_ext": false,
	}
	var maxBitrate int
	if streamType == StreamTypeScreen {
		maxBitrate = int(m.maxScreenBitrate.Load())
	} else {
		maxBitrate = int(m.maxStreamBitrate.Load())
	}
	if bitrate <= 0 {
		bitrate = maxBitrate
	} else {
		bitrate = min(bitrate, maxBitrate)
	}
	create_msg["bitrate"] = bitrate
	create_response, err := handle.Request(ctx, create_msg)
	if err != nil {
		if _, err2 := handle.Detach(ctx); err2 != nil {
			m.log.Error("Error detaching Janus handle",
				zap.Uint64("handle", handle.Id),
				zap.Error(err2),
			)
		}
		return 0, 0, err
	}

	roomId := getPluginIntValue(create_response.PluginData, pluginVideoRoom, "room")
	if roomId == 0 {
		if _, err := handle.Detach(ctx); err != nil {
			m.log.Error("Error detaching Janus handle",
				zap.Uint64("handle", handle.Id),
				zap.Error(err),
			)
		}
		return 0, 0, fmt.Errorf("No room id received: %+v", create_response)
	}

	m.log.Info("Created Janus room",
		zap.Uint64("roomid", roomId),
		zap.Any("data", create_response.PluginData),
	)
	return roomId, bitrate, nil
}

func (m *mcuJanus) getOrCreatePublisherHandle(ctx context.Context, id string, streamType StreamType, bitrate int) (*JanusHandle, uint64, uint64, int, error) {
	session := m.session
	if session == nil {
		return nil, 0, 0, 0, ErrNotConnected
	}
	handle, err := session.Attach(ctx, pluginVideoRoom)
	if err != nil {
		return nil, 0, 0, 0, err
	}

	m.log.Info("Attached Janus publisher",
		zap.Uint64("sessionid", session.Id),
		zap.Any("streamtype", streamType),
		zap.Uint64("handle", handle.Id),
		zap.String("plugin", pluginVideoRoom),
	)

	roomId, bitrate, err := m.createPublisherRoom(ctx, handle, id, streamType, bitrate)
	if err != nil {
		if _, err2 := handle.Detach(ctx); err2 != nil {
			m.log.Error("Error detaching Janus handle",
				zap.Uint64("handle", handle.Id),
				zap.Error(err2),
			)
		}
		return nil, 0, 0, 0, err
	}

	msg := map[string]interface{}{
		"request": "join",
		"ptype":   "publisher",
		"room":    roomId,
		"id":      streamTypeUserIds[streamType],
	}

	response, err := handle.Message(ctx, msg, nil)
	if err != nil {
		if _, err2 := handle.Detach(ctx); err2 != nil {
			m.log.Error("Error detaching Janus handle",
				zap.Uint64("handle", handle.Id),
				zap.Error(err2),
			)
		}
		return nil, 0, 0, 0, err
	}

	return handle, response.Session, roomId, bitrate, nil
}

func (m *mcuJanus) NewPublisher(ctx context.Context, listener McuListener, id string, sid string, streamType StreamType, bitrate int, mediaTypes MediaType, initiator McuInitiator) (McuPublisher, error) {
	if _, found := streamTypeUserIds[streamType]; !found {
		return nil, fmt.Errorf("Unsupported stream type %s", streamType)
	}

	handle, session, roomId, maxBitrate, err := m.getOrCreatePublisherHandle(ctx, id, streamType, bitrate)
	if err != nil {
		return nil, err
	}

	client := &mcuJanusPublisher{
		mcuJanusClient: mcuJanusClient{
			baseLog:  m.log,
			mcu:      m,
			listener: listener,

			id:         m.clientId.Add(1),
			session:    session,
			roomId:     roomId,
			sid:        sid,
			streamType: streamType,
			maxBitrate: maxBitrate,

			handle:    handle,
			handleId:  handle.Id,
			closeChan: make(chan struct{}, 1),
			deferred:  make(chan func(), 64),
		},
		sdpReady:   NewCloser(),
		id:         id,
		bitrate:    bitrate,
		mediaTypes: mediaTypes,
	}
	client.mcuJanusClient.handleEvent = client.handleEvent
	client.mcuJanusClient.handleHangup = client.handleHangup
	client.mcuJanusClient.handleDetached = client.handleDetached
	client.mcuJanusClient.handleConnected = client.handleConnected
	client.mcuJanusClient.handleSlowLink = client.handleSlowLink
	client.mcuJanusClient.handleMedia = client.handleMedia
	client.updateLogger()

	m.registerClient(client)
	m.log.Info("Publisher is using handle",
		zap.String("publisherid", client.id),
		zap.Uint64("handle", client.handleId),
	)
	go client.run(handle, client.closeChan)
	m.mu.Lock()
	m.publishers[getStreamId(id, streamType)] = client
	m.publisherCreated.Notify(getStreamId(id, streamType))
	m.mu.Unlock()
	statsPublishersCurrent.WithLabelValues(string(streamType)).Inc()
	statsPublishersTotal.WithLabelValues(string(streamType)).Inc()
	return client, nil
}

func (m *mcuJanus) getPublisher(ctx context.Context, publisher string, streamType StreamType) (*mcuJanusPublisher, error) {
	// Do the direct check immediately as this should be the normal case.
	key := getStreamId(publisher, streamType)
	m.mu.Lock()
	if result, found := m.publishers[key]; found {
		m.mu.Unlock()
		return result, nil
	}

	waiter := m.publisherCreated.NewWaiter(key)
	m.mu.Unlock()
	defer m.publisherCreated.Release(waiter)

	for {
		m.mu.Lock()
		result := m.publishers[key]
		m.mu.Unlock()
		if result != nil {
			return result, nil
		}

		if err := waiter.Wait(ctx); err != nil {
			return nil, err
		}
	}
}

func (m *mcuJanus) getOrCreateSubscriberHandle(ctx context.Context, publisher string, streamType StreamType) (*JanusHandle, *mcuJanusPublisher, error) {
	var pub *mcuJanusPublisher
	var err error
	if pub, err = m.getPublisher(ctx, publisher, streamType); err != nil {
		return nil, nil, err
	}

	session := m.session
	if session == nil {
		return nil, nil, ErrNotConnected
	}

	handle, err := session.Attach(ctx, pluginVideoRoom)
	if err != nil {
		return nil, nil, err
	}

	m.log.Info("Attached Janus subscriber",
		zap.Uint64("sessionid", session.Id),
		zap.Uint64("roomid", pub.roomId),
		zap.String("publisher", publisher),
		zap.String("plugin", pluginVideoRoom),
		zap.Uint64("handle", handle.Id),
	)
	return handle, pub, nil
}

func (m *mcuJanus) NewSubscriber(ctx context.Context, listener McuListener, publisher string, streamType StreamType, initiator McuInitiator) (McuSubscriber, error) {
	if _, found := streamTypeUserIds[streamType]; !found {
		return nil, fmt.Errorf("Unsupported stream type %s", streamType)
	}

	handle, pub, err := m.getOrCreateSubscriberHandle(ctx, publisher, streamType)
	if err != nil {
		return nil, err
	}

	client := &mcuJanusSubscriber{
		mcuJanusClient: mcuJanusClient{
			baseLog:  m.log,
			mcu:      m,
			listener: listener,

			id:         m.clientId.Add(1),
			roomId:     pub.roomId,
			sid:        strconv.FormatUint(handle.Id, 10),
			streamType: streamType,
			maxBitrate: pub.MaxBitrate(),

			handle:    handle,
			handleId:  handle.Id,
			closeChan: make(chan struct{}, 1),
			deferred:  make(chan func(), 64),
		},
		publisher: publisher,
	}
	client.mcuJanusClient.handleEvent = client.handleEvent
	client.mcuJanusClient.handleHangup = client.handleHangup
	client.mcuJanusClient.handleDetached = client.handleDetached
	client.mcuJanusClient.handleConnected = client.handleConnected
	client.mcuJanusClient.handleSlowLink = client.handleSlowLink
	client.mcuJanusClient.handleMedia = client.handleMedia
	client.updateLogger()
	m.registerClient(client)
	go client.run(handle, client.closeChan)
	statsSubscribersCurrent.WithLabelValues(string(streamType)).Inc()
	statsSubscribersTotal.WithLabelValues(string(streamType)).Inc()
	return client, nil
}

func (m *mcuJanus) getOrCreateRemotePublisher(ctx context.Context, controller RemotePublisherController, streamType StreamType, bitrate int) (*mcuJanusRemotePublisher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pub, found := m.remotePublishers[getStreamId(controller.PublisherId(), streamType)]
	if found {
		return pub, nil
	}

	streams, err := controller.GetStreams(ctx)
	if err != nil {
		return nil, err
	}

	if len(streams) == 0 {
		return nil, errors.New("remote publisher has no streams")
	}

	session := m.session
	if session == nil {
		return nil, ErrNotConnected
	}

	handle, err := session.Attach(ctx, pluginVideoRoom)
	if err != nil {
		return nil, err
	}

	roomId, bitrate, err := m.createPublisherRoom(ctx, handle, controller.PublisherId(), streamType, bitrate)
	if err != nil {
		if _, err2 := handle.Detach(ctx); err2 != nil {
			m.log.Error("Error detaching Janus handle",
				zap.Uint64("handle", handle.Id),
				zap.Error(err2),
			)
		}
		return nil, err
	}

	response, err := handle.Request(ctx, map[string]interface{}{
		"request": "add_remote_publisher",
		"room":    roomId,
		"id":      streamTypeUserIds[streamType],
		"streams": streams,
	})
	if err != nil {
		if _, err2 := handle.Detach(ctx); err2 != nil {
			m.log.Error("Error detaching Janus handle",
				zap.Uint64("handle", handle.Id),
				zap.Error(err2),
			)
		}
		return nil, err
	}

	id := getPluginIntValue(response.PluginData, pluginVideoRoom, "id")
	port := getPluginIntValue(response.PluginData, pluginVideoRoom, "port")
	rtcp_port := getPluginIntValue(response.PluginData, pluginVideoRoom, "rtcp_port")

	pub = &mcuJanusRemotePublisher{
		mcuJanusPublisher: mcuJanusPublisher{
			mcuJanusClient: mcuJanusClient{
				baseLog: m.log,
				mcu:     m,

				id:         id,
				session:    response.Session,
				roomId:     roomId,
				sid:        strconv.FormatUint(handle.Id, 10),
				streamType: streamType,
				maxBitrate: bitrate,

				handle:    handle,
				handleId:  handle.Id,
				closeChan: make(chan struct{}, 1),
				deferred:  make(chan func(), 64),
			},

			sdpReady: NewCloser(),
			id:       controller.PublisherId(),
		},

		port:     int(port),
		rtcpPort: int(rtcp_port),
	}
	pub.mcuJanusClient.handleEvent = pub.handleEvent
	pub.mcuJanusClient.handleHangup = pub.handleHangup
	pub.mcuJanusClient.handleDetached = pub.handleDetached
	pub.mcuJanusClient.handleConnected = pub.handleConnected
	pub.mcuJanusClient.handleSlowLink = pub.handleSlowLink
	pub.mcuJanusClient.handleMedia = pub.handleMedia
	pub.updateLogger()

	if err := controller.StartPublishing(ctx, pub); err != nil {
		go pub.Close(context.Background())
		return nil, err
	}

	m.remotePublishers[getStreamId(controller.PublisherId(), streamType)] = pub

	return pub, nil
}

func (m *mcuJanus) NewRemotePublisher(ctx context.Context, listener McuListener, controller RemotePublisherController, streamType StreamType) (McuRemotePublisher, error) {
	if _, found := streamTypeUserIds[streamType]; !found {
		return nil, fmt.Errorf("Unsupported stream type %s", streamType)
	}

	if !m.hasRemotePublisher() {
		return nil, ErrRemoteStreamsNotSupported
	}

	pub, err := m.getOrCreateRemotePublisher(ctx, controller, streamType, 0)
	if err != nil {
		return nil, err
	}

	pub.addRef()
	return pub, nil
}

func (m *mcuJanus) NewRemoteSubscriber(ctx context.Context, listener McuListener, publisher McuRemotePublisher) (McuRemoteSubscriber, error) {
	pub, ok := publisher.(*mcuJanusRemotePublisher)
	if !ok {
		return nil, errors.New("unsupported remote publisher")
	}

	session := m.session
	if session == nil {
		return nil, ErrNotConnected
	}

	handle, err := session.Attach(ctx, pluginVideoRoom)
	if err != nil {
		return nil, err
	}

	m.log.Info("Attached remote Janus subscriber",
		zap.Uint64("sessionid", session.Id),
		zap.Uint64("roomid", pub.roomId),
		zap.String("publisher", pub.id),
		zap.String("plugin", pluginVideoRoom),
		zap.Uint64("handle", handle.Id),
	)

	client := &mcuJanusRemoteSubscriber{
		mcuJanusSubscriber: mcuJanusSubscriber{
			mcuJanusClient: mcuJanusClient{
				baseLog:  m.log,
				mcu:      m,
				listener: listener,

				id:         m.clientId.Add(1),
				roomId:     pub.roomId,
				sid:        strconv.FormatUint(handle.Id, 10),
				streamType: publisher.StreamType(),
				maxBitrate: pub.MaxBitrate(),

				handle:    handle,
				handleId:  handle.Id,
				closeChan: make(chan struct{}, 1),
				deferred:  make(chan func(), 64),
			},
			publisher: pub.id,
		},
	}
	client.remote.Store(pub)
	pub.addRef()
	client.mcuJanusClient.handleEvent = client.handleEvent
	client.mcuJanusClient.handleHangup = client.handleHangup
	client.mcuJanusClient.handleDetached = client.handleDetached
	client.mcuJanusClient.handleConnected = client.handleConnected
	client.mcuJanusClient.handleSlowLink = client.handleSlowLink
	client.mcuJanusClient.handleMedia = client.handleMedia
	client.updateLogger()
	m.registerClient(client)
	go client.run(handle, client.closeChan)
	statsSubscribersCurrent.WithLabelValues(string(publisher.StreamType())).Inc()
	statsSubscribersTotal.WithLabelValues(string(publisher.StreamType())).Inc()
	return client, nil
}
