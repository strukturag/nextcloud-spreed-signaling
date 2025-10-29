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
	"log"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"github.com/notedit/janus-go"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
)

const (
	pluginVideoRoom = "janus.plugin.videoroom"
	eventWebsocket  = "janus.eventhandler.wsevh"

	keepaliveInterval = 30 * time.Second
	bandwidthInterval = time.Second

	videoPublisherUserId  = 1
	screenPublisherUserId = 2

	initialReconnectInterval = 1 * time.Second
	maxReconnectInterval     = 16 * time.Second
)

var (
	ErrRemoteStreamsNotSupported = errors.New("need Janus 1.1.0 for remote streams")

	streamTypeUserIds = map[StreamType]uint64{
		StreamTypeVideo:  videoPublisherUserId,
		StreamTypeScreen: screenPublisherUserId,
	}
)

type StreamId string

func getStreamId(publisherId PublicSessionId, streamType StreamType) StreamId {
	return StreamId(fmt.Sprintf("%s|%s", publisherId, streamType))
}

func getPluginValue(data janus.PluginData, pluginName string, key string) any {
	if data.Plugin != pluginName {
		return nil
	}

	return data.Data[key]
}

func convertIntValue(value any) (uint64, error) {
	switch t := value.(type) {
	case float64:
		if t < 0 {
			return 0, fmt.Errorf("unsupported float64 number: %+v", t)
		}
		return uint64(t), nil
	case uint64:
		return t, nil
	case int:
		if t < 0 {
			return 0, fmt.Errorf("unsupported int number: %+v", t)
		}
		return uint64(t), nil
	case int64:
		if t < 0 {
			return 0, fmt.Errorf("unsupported int64 number: %+v", t)
		}
		return uint64(t), nil
	case json.Number:
		r, err := t.Int64()
		if err != nil {
			return 0, err
		} else if r < 0 {
			return 0, fmt.Errorf("unsupported JSON number: %+v", t)
		}
		return uint64(r), nil
	default:
		return 0, fmt.Errorf("unknown number type: %+v (%T)", t, t)
	}
}

func getPluginIntValue(data janus.PluginData, pluginName string, key string) uint64 {
	val := getPluginValue(data, pluginName, key)
	if val == nil {
		return 0
	}

	result, err := convertIntValue(val)
	if err != nil {
		log.Printf("Invalid value %+v for %s: %s", val, key, err)
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
	Handle() uint64

	NotifyReconnected()

	Bandwidth() *McuClientBandwidthInfo
	UpdateBandwidth(media string, sent uint32, received uint32)
}

type mcuJanusSettings struct {
	mcuCommonSettings

	allowedCandidates atomic.Pointer[AllowedIps]
	blockedCandidates atomic.Pointer[AllowedIps]
}

func newMcuJanusSettings(config *goconf.ConfigFile) (*mcuJanusSettings, error) {
	settings := &mcuJanusSettings{}
	if err := settings.load(config); err != nil {
		return nil, err
	}

	return settings, nil
}

func (s *mcuJanusSettings) load(config *goconf.ConfigFile) error {
	if err := s.mcuCommonSettings.load(config); err != nil {
		return err
	}

	mcuTimeoutSeconds, _ := config.GetInt("mcu", "timeout")
	if mcuTimeoutSeconds <= 0 {
		mcuTimeoutSeconds = defaultMcuTimeoutSeconds
	}
	mcuTimeout := time.Duration(mcuTimeoutSeconds) * time.Second
	log.Printf("Using a timeout of %s for MCU requests", mcuTimeout)
	s.setTimeout(mcuTimeout)

	if value, _ := config.GetString("mcu", "allowedcandidates"); value != "" {
		allowed, err := ParseAllowedIps(value)
		if err != nil {
			return fmt.Errorf("invalid allowedcandidates: %w", err)
		}

		log.Printf("Candidates allowlist: %s", allowed)
		s.allowedCandidates.Store(allowed)
	} else {
		log.Printf("No candidates allowlist")
		s.allowedCandidates.Store(nil)
	}
	if value, _ := config.GetString("mcu", "blockedcandidates"); value != "" {
		blocked, err := ParseAllowedIps(value)
		if err != nil {
			return fmt.Errorf("invalid blockedcandidates: %w", err)
		}

		log.Printf("Candidates blocklist: %s", blocked)
		s.blockedCandidates.Store(blocked)
	} else {
		log.Printf("No candidates blocklist")
		s.blockedCandidates.Store(nil)
	}

	return nil
}

func (s *mcuJanusSettings) Reload(config *goconf.ConfigFile) {
	if err := s.load(config); err != nil {
		log.Printf("Error reloading MCU settings: %s", err)
	}
}

type mcuJanus struct {
	url string
	mu  sync.Mutex

	settings *mcuJanusSettings

	createJanusGateway func(ctx context.Context, wsURL string, listener GatewayListener) (JanusGatewayInterface, error)

	gw      JanusGatewayInterface
	session *JanusSession
	handle  *JanusHandle

	version int
	info    atomic.Pointer[InfoMsg]

	closeChan chan struct{}

	muClients sync.RWMutex
	// +checklocks:muClients
	clients  map[uint64]clientInterface
	clientId atomic.Uint64

	// +checklocks:mu
	publishers         map[StreamId]*mcuJanusPublisher
	publisherCreated   Notifier
	publisherConnected Notifier
	// +checklocks:mu
	remotePublishers map[StreamId]*mcuJanusRemotePublisher

	reconnectTimer    *time.Timer
	reconnectInterval time.Duration

	connectedSince time.Time
	onConnected    atomic.Value
	onDisconnected atomic.Value
}

func emptyOnConnected()    {}
func emptyOnDisconnected() {}

func NewMcuJanus(ctx context.Context, url string, config *goconf.ConfigFile) (Mcu, error) {
	settings, err := newMcuJanusSettings(config)
	if err != nil {
		return nil, err
	}

	mcu := &mcuJanus{
		url:       url,
		settings:  settings,
		closeChan: make(chan struct{}, 1),
		clients:   make(map[uint64]clientInterface),

		publishers:       make(map[StreamId]*mcuJanusPublisher),
		remotePublishers: make(map[StreamId]*mcuJanusRemotePublisher),

		createJanusGateway: func(ctx context.Context, wsURL string, listener GatewayListener) (JanusGatewayInterface, error) {
			return NewJanusGateway(ctx, wsURL, listener)
		},
		reconnectInterval: initialReconnectInterval,
	}
	mcu.onConnected.Store(emptyOnConnected)
	mcu.onDisconnected.Store(emptyOnDisconnected)

	mcu.reconnectTimer = time.AfterFunc(mcu.reconnectInterval, func() {
		mcu.doReconnect(context.Background())
	})
	mcu.reconnectTimer.Stop()
	if mcu.url != "" {
		if err := mcu.reconnect(ctx); err != nil {
			return nil, err
		}
	}
	return mcu, nil
}

func (m *mcuJanus) disconnect() {
	if handle := m.handle; handle != nil {
		m.handle = nil
		m.closeChan <- struct{}{}
		if _, err := handle.Detach(context.TODO()); err != nil {
			log.Printf("Error detaching handle %d: %s", handle.Id, err)
		}
	}
	if m.session != nil {
		if _, err := m.session.Destroy(context.TODO()); err != nil {
			log.Printf("Error destroying session %d: %s", m.session.Id, err)
		}
		m.session = nil
	}
	if m.gw != nil {
		if err := m.gw.Close(); err != nil {
			log.Println("Error while closing connection to MCU", err)
		}
		m.gw = nil
	}
}

func (m *mcuJanus) GetBandwidthLimits() (int, int) {
	return int(m.settings.MaxStreamBitrate()), int(m.settings.MaxScreenBitrate())
}

func (m *mcuJanus) Bandwidth() (result *McuClientBandwidthInfo) {
	m.muClients.RLock()
	defer m.muClients.RUnlock()

	for _, client := range m.clients {
		if bandwidth := client.Bandwidth(); bandwidth != nil {
			if result == nil {
				result = &McuClientBandwidthInfo{}
			}
			result.Received += bandwidth.Received
			result.Sent += bandwidth.Sent
		}
	}
	return
}

func (m *mcuJanus) updateBandwidthStats() {
	if info := m.info.Load(); info != nil {
		if !info.EventHandlers {
			// Event handlers are disabled, no stats will be available.
			return
		}

		if _, found := info.Events[eventWebsocket]; !found {
			// Event handler plugin not found, no stats will be available.
			return
		}
	}

	if bandwidth := m.Bandwidth(); bandwidth != nil {
		statsJanusBandwidthCurrent.WithLabelValues("incoming").Set(float64(bandwidth.Received))
		statsJanusBandwidthCurrent.WithLabelValues("outgoing").Set(float64(bandwidth.Sent))
	} else {
		statsJanusBandwidthCurrent.WithLabelValues("incoming").Set(0)
		statsJanusBandwidthCurrent.WithLabelValues("outgoing").Set(0)
	}
}

func (m *mcuJanus) reconnect(ctx context.Context) error {
	m.disconnect()
	gw, err := m.createJanusGateway(ctx, m.url, m)
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

	log.Println("Reconnection to Janus gateway successful")
	m.mu.Lock()
	clear(m.publishers)
	m.publisherCreated.Reset()
	m.publisherConnected.Reset()
	m.reconnectInterval = initialReconnectInterval
	m.mu.Unlock()

	m.notifyClientsReconnected()
}

func (m *mcuJanus) notifyClientsReconnected() {
	m.muClients.RLock()
	defer m.muClients.RUnlock()

	for oldHandle, client := range m.clients {
		go func(oldHandle uint64, client clientInterface) {
			client.NotifyReconnected()
			newHandle := client.Handle()

			if oldHandle != newHandle {
				m.muClients.Lock()
				defer m.muClients.Unlock()

				delete(m.clients, oldHandle)
				m.clients[newHandle] = client
			}
		}(oldHandle, client)
	}
}

func (m *mcuJanus) scheduleReconnect(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconnectTimer.Reset(m.reconnectInterval)
	if err == nil {
		log.Printf("Connection to Janus gateway was interrupted, reconnecting in %s", m.reconnectInterval)
	} else {
		log.Printf("Reconnect to Janus gateway failed (%s), reconnecting in %s", err, m.reconnectInterval)
	}

	m.reconnectInterval = min(m.reconnectInterval*2, maxReconnectInterval)
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
	if m.url == "" {
		if err := m.reconnect(ctx); err != nil {
			return err
		}
	}
	info, err := m.gw.Info(ctx)
	if err != nil {
		return err
	}

	log.Printf("Connected to %s %s by %s", info.Name, info.VersionString, info.Author)
	m.version = info.Version

	if plugin, found := info.Plugins[pluginVideoRoom]; found {
		log.Printf("Found %s %s by %s", plugin.Name, plugin.VersionString, plugin.Author)
	} else {
		return fmt.Errorf("plugin %s is not supported", pluginVideoRoom)
	}

	if plugin, found := info.Events[eventWebsocket]; found {
		if !info.EventHandlers {
			log.Printf("Found %s %s by %s but event handlers are disabled, realtime usage will not be available", plugin.Name, plugin.VersionString, plugin.Author)
		} else {
			log.Printf("Found %s %s by %s", plugin.Name, plugin.VersionString, plugin.Author)
		}
	} else {
		log.Printf("Plugin %s not found, realtime usage will not be available", eventWebsocket)
	}

	log.Printf("Used dependencies: %+v", info.Dependencies)
	if !info.DataChannels {
		return fmt.Errorf("data channels are not supported")
	}

	log.Println("Data channels are supported")
	if !info.FullTrickle {
		log.Println("WARNING: Full-Trickle is NOT enabled in Janus!")
	} else {
		log.Println("Full-Trickle is enabled")
	}

	if m.session, err = m.gw.Create(ctx); err != nil {
		m.disconnect()
		return err
	}
	log.Println("Created Janus session", m.session.Id)
	m.connectedSince = time.Now()

	if m.handle, err = m.session.Attach(ctx, pluginVideoRoom); err != nil {
		m.disconnect()
		return err
	}
	log.Println("Created Janus handle", m.handle.Id)

	m.info.Store(info)

	go m.run()

	m.notifyOnConnected()
	return nil
}

func (m *mcuJanus) registerClient(client clientInterface) {
	m.muClients.Lock()
	defer m.muClients.Unlock()

	m.clients[client.Handle()] = client
}

func (m *mcuJanus) unregisterClient(client clientInterface) {
	m.muClients.Lock()
	defer m.muClients.Unlock()

	delete(m.clients, client.Handle())
}

func (m *mcuJanus) run() {
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()

	bandwidthTicker := time.NewTicker(bandwidthInterval)
	defer bandwidthTicker.Stop()

loop:
	for {
		select {
		case <-ticker.C:
			m.sendKeepalive(context.Background())
		case <-bandwidthTicker.C:
			m.updateBandwidthStats()
		case <-m.closeChan:
			break loop
		}
	}
}

func (m *mcuJanus) Stop() {
	m.disconnect()
	m.reconnectTimer.Stop()
}

func (m *mcuJanus) IsConnected() bool {
	return m.handle != nil
}

func (m *mcuJanus) Info() *InfoMsg {
	return m.info.Load()
}

func (m *mcuJanus) GetServerInfoSfu() *BackendServerInfoSfu {
	janus := &BackendServerInfoSfuJanus{
		Url: m.url,
	}
	if m.IsConnected() {
		janus.Connected = true
		if info := m.Info(); info != nil {
			janus.Name = info.Name
			janus.Version = info.VersionString
			janus.Author = info.Author
			janus.DataChannels = internal.MakePtr(info.DataChannels)
			janus.FullTrickle = internal.MakePtr(info.FullTrickle)
			janus.LocalIP = info.LocalIP
			janus.IPv6 = internal.MakePtr(info.IPv6)

			if plugin, found := info.Plugins[pluginVideoRoom]; found {
				janus.VideoRoom = &BackendServerInfoVideoRoom{
					Name:    plugin.Name,
					Version: plugin.VersionString,
					Author:  plugin.Author,
				}
			}
		}
	}

	sfu := &BackendServerInfoSfu{
		Mode:  SfuModeJanus,
		Janus: janus,
	}
	return sfu
}

func (m *mcuJanus) Reload(config *goconf.ConfigFile) {
	m.settings.Reload(config)
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

func (m *mcuJanus) GetStats() any {
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
		log.Println("Could not send keepalive request", err)
		if e, ok := err.(*janus.ErrorMsg); ok {
			switch e.Err.Code {
			case JANUS_ERROR_SESSION_NOT_FOUND:
				m.scheduleReconnect(err)
			}
		}
	}
}

func (m *mcuJanus) SubscriberConnected(id string, publisher PublicSessionId, streamType StreamType) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p, found := m.publishers[getStreamId(publisher, streamType)]; found {
		p.stats.AddSubscriber(id)
	}
}

func (m *mcuJanus) SubscriberDisconnected(id string, publisher PublicSessionId, streamType StreamType) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p, found := m.publishers[getStreamId(publisher, streamType)]; found {
		p.stats.RemoveSubscriber(id)
	}
}

func (m *mcuJanus) createPublisherRoom(ctx context.Context, handle *JanusHandle, id PublicSessionId, streamType StreamType, settings NewPublisherSettings) (uint64, int, error) {
	create_msg := api.StringMap{
		"request":     "create",
		"description": getStreamId(id, streamType),
		// We publish every stream in its own Janus room.
		"publishers": 1,
		// Do not use the video-orientation RTP extension as it breaks video
		// orientation changes in Firefox.
		"videoorient_ext": false,
	}
	if codec := settings.AudioCodec; codec != "" {
		create_msg["audiocodec"] = codec
	}
	if codec := settings.VideoCodec; codec != "" {
		create_msg["videocodec"] = codec
	}
	if profile := settings.VP9Profile; profile != "" {
		create_msg["vp9_profile"] = profile
	}
	if profile := settings.H264Profile; profile != "" {
		create_msg["h264_profile"] = profile
	}
	var maxBitrate int
	if streamType == StreamTypeScreen {
		maxBitrate = int(m.settings.MaxScreenBitrate())
	} else {
		maxBitrate = int(m.settings.MaxStreamBitrate())
	}
	bitrate := settings.Bitrate
	if bitrate <= 0 {
		bitrate = maxBitrate
	} else {
		bitrate = min(bitrate, maxBitrate)
	}
	create_msg["bitrate"] = bitrate
	create_response, err := handle.Request(ctx, create_msg)
	if err != nil {
		if _, err2 := handle.Detach(ctx); err2 != nil {
			log.Printf("Error detaching handle %d: %s", handle.Id, err2)
		}
		return 0, 0, err
	}

	roomId := getPluginIntValue(create_response.PluginData, pluginVideoRoom, "room")
	if roomId == 0 {
		if _, err := handle.Detach(ctx); err != nil {
			log.Printf("Error detaching handle %d: %s", handle.Id, err)
		}
		return 0, 0, fmt.Errorf("no room id received: %+v", create_response)
	}

	log.Println("Created room", roomId, create_response.PluginData)
	return roomId, bitrate, nil
}

func (m *mcuJanus) getOrCreatePublisherHandle(ctx context.Context, id PublicSessionId, streamType StreamType, settings NewPublisherSettings) (*JanusHandle, uint64, uint64, int, error) {
	session := m.session
	if session == nil {
		return nil, 0, 0, 0, ErrNotConnected
	}
	handle, err := session.Attach(ctx, pluginVideoRoom)
	if err != nil {
		return nil, 0, 0, 0, err
	}

	log.Printf("Attached %s as publisher %d to plugin %s in session %d", streamType, handle.Id, pluginVideoRoom, session.Id)

	roomId, bitrate, err := m.createPublisherRoom(ctx, handle, id, streamType, settings)
	if err != nil {
		if _, err2 := handle.Detach(ctx); err2 != nil {
			log.Printf("Error detaching handle %d: %s", handle.Id, err2)
		}
		return nil, 0, 0, 0, err
	}

	msg := api.StringMap{
		"request": "join",
		"ptype":   "publisher",
		"room":    roomId,
		"id":      streamTypeUserIds[streamType],
	}

	response, err := handle.Message(ctx, msg, nil)
	if err != nil {
		if _, err2 := handle.Detach(ctx); err2 != nil {
			log.Printf("Error detaching handle %d: %s", handle.Id, err2)
		}
		return nil, 0, 0, 0, err
	}

	return handle, response.Session, roomId, bitrate, nil
}

func (m *mcuJanus) NewPublisher(ctx context.Context, listener McuListener, id PublicSessionId, sid string, streamType StreamType, settings NewPublisherSettings, initiator McuInitiator) (McuPublisher, error) {
	if _, found := streamTypeUserIds[streamType]; !found {
		return nil, fmt.Errorf("unsupported stream type %s", streamType)
	}

	handle, session, roomId, maxBitrate, err := m.getOrCreatePublisherHandle(ctx, id, streamType, settings)
	if err != nil {
		return nil, err
	}

	client := &mcuJanusPublisher{
		mcuJanusClient: mcuJanusClient{
			mcu:      m,
			listener: listener,

			id:         m.clientId.Add(1),
			session:    session,
			roomId:     roomId,
			sid:        sid,
			streamType: streamType,
			maxBitrate: maxBitrate,

			closeChan: make(chan struct{}, 1),
			deferred:  make(chan func(), 64),
		},
		sdpReady: NewCloser(),
		id:       id,
		settings: settings,
	}
	client.handle.Store(handle)
	client.handleId.Store(handle.Id)
	client.mcuJanusClient.handleEvent = client.handleEvent
	client.mcuJanusClient.handleHangup = client.handleHangup
	client.mcuJanusClient.handleDetached = client.handleDetached
	client.mcuJanusClient.handleConnected = client.handleConnected
	client.mcuJanusClient.handleSlowLink = client.handleSlowLink
	client.mcuJanusClient.handleMedia = client.handleMedia

	m.registerClient(client)
	log.Printf("Publisher %s is using handle %d", client.id, handle.Id)
	go client.run(handle, client.closeChan)
	m.mu.Lock()
	m.publishers[getStreamId(id, streamType)] = client
	m.publisherCreated.Notify(string(getStreamId(id, streamType)))
	m.mu.Unlock()
	statsPublishersCurrent.WithLabelValues(string(streamType)).Inc()
	statsPublishersTotal.WithLabelValues(string(streamType)).Inc()
	return client, nil
}

func (m *mcuJanus) getPublisher(ctx context.Context, publisher PublicSessionId, streamType StreamType) (*mcuJanusPublisher, error) {
	// Do the direct check immediately as this should be the normal case.
	key := getStreamId(publisher, streamType)
	m.mu.Lock()
	if result, found := m.publishers[key]; found {
		m.mu.Unlock()
		return result, nil
	}

	waiter := m.publisherCreated.NewWaiter(string(key))
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

func (m *mcuJanus) getOrCreateSubscriberHandle(ctx context.Context, publisher PublicSessionId, streamType StreamType) (*JanusHandle, *mcuJanusPublisher, error) {
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

	log.Printf("Attached subscriber to room %d of publisher %s in plugin %s in session %d as %d", pub.roomId, publisher, pluginVideoRoom, session.Id, handle.Id)
	return handle, pub, nil
}

func (m *mcuJanus) NewSubscriber(ctx context.Context, listener McuListener, publisher PublicSessionId, streamType StreamType, initiator McuInitiator) (McuSubscriber, error) {
	if _, found := streamTypeUserIds[streamType]; !found {
		return nil, fmt.Errorf("unsupported stream type %s", streamType)
	}

	handle, pub, err := m.getOrCreateSubscriberHandle(ctx, publisher, streamType)
	if err != nil {
		return nil, err
	}

	client := &mcuJanusSubscriber{
		mcuJanusClient: mcuJanusClient{
			mcu:      m,
			listener: listener,

			id:         m.clientId.Add(1),
			roomId:     pub.roomId,
			sid:        strconv.FormatUint(handle.Id, 10),
			streamType: streamType,
			maxBitrate: pub.MaxBitrate(),

			closeChan: make(chan struct{}, 1),
			deferred:  make(chan func(), 64),
		},
		publisher: publisher,
	}
	client.handle.Store(handle)
	client.handleId.Store(handle.Id)
	client.mcuJanusClient.handleEvent = client.handleEvent
	client.mcuJanusClient.handleHangup = client.handleHangup
	client.mcuJanusClient.handleDetached = client.handleDetached
	client.mcuJanusClient.handleConnected = client.handleConnected
	client.mcuJanusClient.handleSlowLink = client.handleSlowLink
	client.mcuJanusClient.handleMedia = client.handleMedia
	m.registerClient(client)
	go client.run(handle, client.closeChan)
	statsSubscribersCurrent.WithLabelValues(string(streamType)).Inc()
	statsSubscribersTotal.WithLabelValues(string(streamType)).Inc()
	return client, nil
}

func (m *mcuJanus) getOrCreateRemotePublisher(ctx context.Context, controller RemotePublisherController, streamType StreamType, settings NewPublisherSettings) (*mcuJanusRemotePublisher, error) {
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

	roomId, maxBitrate, err := m.createPublisherRoom(ctx, handle, controller.PublisherId(), streamType, settings)
	if err != nil {
		if _, err2 := handle.Detach(ctx); err2 != nil {
			log.Printf("Error detaching handle %d: %s", handle.Id, err2)
		}
		return nil, err
	}

	response, err := handle.Request(ctx, api.StringMap{
		"request": "add_remote_publisher",
		"room":    roomId,
		"id":      streamTypeUserIds[streamType],
		"streams": streams,
	})
	if err != nil {
		if _, err2 := handle.Detach(ctx); err2 != nil {
			log.Printf("Error detaching handle %d: %s", handle.Id, err2)
		}
		return nil, err
	}

	id := getPluginIntValue(response.PluginData, pluginVideoRoom, "id")
	port := getPluginIntValue(response.PluginData, pluginVideoRoom, "port")
	rtcp_port := getPluginIntValue(response.PluginData, pluginVideoRoom, "rtcp_port")

	pub = &mcuJanusRemotePublisher{
		mcuJanusPublisher: mcuJanusPublisher{
			mcuJanusClient: mcuJanusClient{
				mcu: m,

				id:         id,
				session:    response.Session,
				roomId:     roomId,
				sid:        strconv.FormatUint(handle.Id, 10),
				streamType: streamType,
				maxBitrate: maxBitrate,

				closeChan: make(chan struct{}, 1),
				deferred:  make(chan func(), 64),
			},

			sdpReady: NewCloser(),
			id:       controller.PublisherId(),
			settings: settings,
		},

		controller: controller,

		port:     int(port),
		rtcpPort: int(rtcp_port),
	}
	pub.handle.Store(handle)
	pub.handleId.Store(handle.Id)
	pub.mcuJanusClient.handleEvent = pub.handleEvent
	pub.mcuJanusClient.handleHangup = pub.handleHangup
	pub.mcuJanusClient.handleDetached = pub.handleDetached
	pub.mcuJanusClient.handleConnected = pub.handleConnected
	pub.mcuJanusClient.handleSlowLink = pub.handleSlowLink
	pub.mcuJanusClient.handleMedia = pub.handleMedia

	if err := controller.StartPublishing(ctx, pub); err != nil {
		go pub.Close(context.Background())
		return nil, err
	}

	m.remotePublishers[getStreamId(controller.PublisherId(), streamType)] = pub

	return pub, nil
}

func (m *mcuJanus) NewRemotePublisher(ctx context.Context, listener McuListener, controller RemotePublisherController, streamType StreamType) (McuRemotePublisher, error) {
	if _, found := streamTypeUserIds[streamType]; !found {
		return nil, fmt.Errorf("unsupported stream type %s", streamType)
	}

	if !m.hasRemotePublisher() {
		return nil, ErrRemoteStreamsNotSupported
	}

	pub, err := m.getOrCreateRemotePublisher(ctx, controller, streamType, NewPublisherSettings{})
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

	log.Printf("Attached subscriber to room %d of publisher %s in plugin %s in session %d as %d", pub.roomId, pub.id, pluginVideoRoom, session.Id, handle.Id)

	client := &mcuJanusRemoteSubscriber{
		mcuJanusSubscriber: mcuJanusSubscriber{
			mcuJanusClient: mcuJanusClient{
				mcu:      m,
				listener: listener,

				id:         m.clientId.Add(1),
				roomId:     pub.roomId,
				sid:        strconv.FormatUint(handle.Id, 10),
				streamType: publisher.StreamType(),
				maxBitrate: pub.MaxBitrate(),

				closeChan: make(chan struct{}, 1),
				deferred:  make(chan func(), 64),
			},
			publisher: pub.id,
		},
	}
	client.remote.Store(pub)
	pub.addRef()
	client.handle.Store(handle)
	client.handleId.Store(handle.Id)
	client.mcuJanusClient.handleEvent = client.handleEvent
	client.mcuJanusClient.handleHangup = client.handleHangup
	client.mcuJanusClient.handleDetached = client.handleDetached
	client.mcuJanusClient.handleConnected = client.handleConnected
	client.mcuJanusClient.handleSlowLink = client.handleSlowLink
	client.mcuJanusClient.handleMedia = client.handleMedia
	m.registerClient(client)
	go client.run(handle, client.closeChan)
	statsSubscribersCurrent.WithLabelValues(string(publisher.StreamType())).Inc()
	statsSubscribersTotal.WithLabelValues(string(publisher.StreamType())).Inc()
	return client, nil
}

func (m *mcuJanus) UpdateBandwidth(handle uint64, media string, sent uint32, received uint32) {
	m.muClients.RLock()
	defer m.muClients.RUnlock()

	client, found := m.clients[handle]
	if !found {
		return
	}

	client.UpdateBandwidth(media, sent, received)
}
