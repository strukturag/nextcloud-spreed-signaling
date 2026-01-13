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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/async"
	"github.com/strukturag/nextcloud-spreed-signaling/container"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/log"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu"
	sfuinternal "github.com/strukturag/nextcloud-spreed-signaling/sfu/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/sfu/janus/janus"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
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

	// MCU requests will be cancelled if they take too long.
	defaultMcuTimeoutSeconds = 10
)

var (
	ErrRemoteStreamsNotSupported = errors.New("need Janus 1.1.0 for remote streams")

	streamTypeUserIds = map[sfu.StreamType]uint64{
		sfu.StreamTypeVideo:  videoPublisherUserId,
		sfu.StreamTypeScreen: screenPublisherUserId,
	}
)

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

func getPluginIntValue(logger log.Logger, data janus.PluginData, pluginName string, key string) uint64 {
	val := getPluginValue(data, pluginName, key)
	if val == nil {
		return 0
	}

	result, err := convertIntValue(val)
	if err != nil {
		logger.Printf("Invalid value %+v for %s: %s", val, key, err)
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

	Bandwidth() *sfu.ClientBandwidthInfo
	UpdateBandwidth(media string, sent api.Bandwidth, received api.Bandwidth)
}

type Settings struct {
	sfuinternal.CommonSettings

	allowedCandidates atomic.Pointer[container.IPList]
	blockedCandidates atomic.Pointer[container.IPList]
}

func newJanusSettings(ctx context.Context, config *goconf.ConfigFile) (*Settings, error) {
	settings := &Settings{
		CommonSettings: sfuinternal.CommonSettings{
			Logger: log.LoggerFromContext(ctx),
		},
	}
	if err := settings.load(config); err != nil {
		return nil, err
	}

	return settings, nil
}

func (s *Settings) load(config *goconf.ConfigFile) error {
	if err := s.Load(config); err != nil {
		return err
	}

	mcuTimeoutSeconds, _ := config.GetInt("mcu", "timeout")
	if mcuTimeoutSeconds <= 0 {
		mcuTimeoutSeconds = defaultMcuTimeoutSeconds
	}
	mcuTimeout := time.Duration(mcuTimeoutSeconds) * time.Second
	s.Logger.Printf("Using a timeout of %s for MCU requests", mcuTimeout)
	s.SetTimeout(mcuTimeout)

	if value, _ := config.GetString("mcu", "allowedcandidates"); value != "" {
		allowed, err := container.ParseIPList(value)
		if err != nil {
			return fmt.Errorf("invalid allowedcandidates: %w", err)
		}

		s.Logger.Printf("Candidates allowlist: %s", allowed)
		s.allowedCandidates.Store(allowed)
	} else {
		s.Logger.Printf("No candidates allowlist")
		s.allowedCandidates.Store(nil)
	}
	if value, _ := config.GetString("mcu", "blockedcandidates"); value != "" {
		blocked, err := container.ParseIPList(value)
		if err != nil {
			return fmt.Errorf("invalid blockedcandidates: %w", err)
		}

		s.Logger.Printf("Candidates blocklist: %s", blocked)
		s.blockedCandidates.Store(blocked)
	} else {
		s.Logger.Printf("No candidates blocklist")
		s.blockedCandidates.Store(nil)
	}

	return nil
}

func (s *Settings) Reload(config *goconf.ConfigFile) {
	if err := s.load(config); err != nil {
		s.Logger.Printf("Error reloading MCU settings: %s", err)
	}
}

type Stats interface {
	IncSubscriber(streamType sfu.StreamType)
	DecSubscriber(streamType sfu.StreamType)
}

type prometheusJanusStats struct{}

func (s *prometheusJanusStats) IncSubscriber(streamType sfu.StreamType) {
	sfuinternal.StatsSubscribersCurrent.WithLabelValues(string(streamType)).Inc()
}

func (s *prometheusJanusStats) DecSubscriber(streamType sfu.StreamType) {
	sfuinternal.StatsSubscribersCurrent.WithLabelValues(string(streamType)).Dec()
}

type janusSFU struct {
	logger log.Logger

	url string
	mu  sync.Mutex

	settings *Settings
	stats    Stats

	createJanusGateway func(ctx context.Context, wsURL string, listener janus.GatewayListener) (janus.GatewayInterface, error)

	gw      janus.GatewayInterface
	session *janus.Session
	handle  *janus.Handle

	version int
	info    atomic.Pointer[janus.InfoMsg]

	closeChan chan struct{}

	muClients sync.RWMutex
	// +checklocks:muClients
	clients  map[uint64]clientInterface
	clientId atomic.Uint64

	// +checklocks:mu
	publishers         map[sfu.StreamId]*janusPublisher
	publisherCreated   async.Notifier
	publisherConnected async.Notifier
	// +checklocks:mu
	remotePublishers map[sfu.StreamId]*janusRemotePublisher

	reconnectTimer    *time.Timer
	reconnectInterval time.Duration

	connectedSince time.Time
	onConnected    atomic.Value
	onDisconnected atomic.Value
}

func emptyOnConnected()    {}
func emptyOnDisconnected() {}

func NewJanusSFU(ctx context.Context, url string, config *goconf.ConfigFile) (sfu.SFU, error) {
	settings, err := newJanusSettings(ctx, config)
	if err != nil {
		return nil, err
	}

	mcu := &janusSFU{
		logger:    log.LoggerFromContext(ctx),
		url:       url,
		settings:  settings,
		stats:     &prometheusJanusStats{},
		closeChan: make(chan struct{}, 1),
		clients:   make(map[uint64]clientInterface),

		publishers:       make(map[sfu.StreamId]*janusPublisher),
		remotePublishers: make(map[sfu.StreamId]*janusRemotePublisher),

		createJanusGateway: func(ctx context.Context, wsURL string, listener janus.GatewayListener) (janus.GatewayInterface, error) {
			return janus.NewGateway(ctx, wsURL, listener)
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

func NewJanusSFUWithGateway(ctx context.Context, gateway janus.GatewayInterface, config *goconf.ConfigFile) (sfu.SFU, error) {
	sfu, err := NewJanusSFU(ctx, "", config)
	if err != nil {
		return nil, err
	}

	sfuJanus := sfu.(*janusSFU)
	sfuJanus.createJanusGateway = func(ctx context.Context, wsURL string, listener janus.GatewayListener) (janus.GatewayInterface, error) {
		return gateway, nil
	}
	return sfu, nil
}

func (m *janusSFU) SetStats(stats Stats) {
	m.stats = stats
}

func (m *janusSFU) Settings() *Settings {
	return m.settings
}

func (m *janusSFU) disconnect() {
	if handle := m.handle; handle != nil {
		m.handle = nil
		m.closeChan <- struct{}{}
		if _, err := handle.Detach(context.TODO()); err != nil {
			m.logger.Printf("Error detaching handle %d: %s", handle.Id, err)
		}
	}
	if m.session != nil {
		if _, err := m.session.Destroy(context.TODO()); err != nil {
			m.logger.Printf("Error destroying session %d: %s", m.session.Id, err)
		}
		m.session = nil
	}
	if m.gw != nil {
		if err := m.gw.Close(); err != nil {
			m.logger.Println("Error while closing connection to MCU", err)
		}
		m.gw = nil
	}
}

func (m *janusSFU) GetBandwidthLimits() (api.Bandwidth, api.Bandwidth) {
	return m.settings.MaxStreamBitrate(), m.settings.MaxScreenBitrate()
}

func (m *janusSFU) Bandwidth() (result *sfu.ClientBandwidthInfo) {
	m.muClients.RLock()
	defer m.muClients.RUnlock()

	for _, client := range m.clients {
		if bandwidth := client.Bandwidth(); bandwidth != nil {
			if result == nil {
				result = &sfu.ClientBandwidthInfo{}
			}
			result.Received += bandwidth.Received
			result.Sent += bandwidth.Sent
		}
	}
	return
}

type janusBandwidthStats interface {
	SetBandwidth(incoming uint64, outgoing uint64)
}

type prometheusJanusBandwidthStats struct{}

func (s *prometheusJanusBandwidthStats) SetBandwidth(incoming uint64, outgoing uint64) {
	statsJanusBandwidthCurrent.WithLabelValues("incoming").Set(float64(incoming))
	statsJanusBandwidthCurrent.WithLabelValues("outgoing").Set(float64(outgoing))
}

var (
	defaultJanusBandwidthStats = &prometheusJanusBandwidthStats{}
)

func (m *janusSFU) updateBandwidthStats(stats janusBandwidthStats) {
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

	if stats == nil {
		stats = defaultJanusBandwidthStats
	}

	if bandwidth := m.Bandwidth(); bandwidth != nil {
		stats.SetBandwidth(bandwidth.Received.Bytes(), bandwidth.Sent.Bytes())
	} else {
		stats.SetBandwidth(0, 0)
	}
}

func (m *janusSFU) reconnect(ctx context.Context) error {
	m.disconnect()
	gw, err := m.createJanusGateway(ctx, m.url, m)
	if err != nil {
		return err
	}

	m.gw = gw
	m.reconnectTimer.Stop()
	return nil
}

func (m *janusSFU) doReconnect(ctx context.Context) {
	if err := m.reconnect(ctx); err != nil {
		m.scheduleReconnect(err)
		return
	}
	if err := m.Start(ctx); err != nil {
		m.scheduleReconnect(err)
		return
	}

	m.logger.Println("Reconnection to Janus gateway successful")
	m.mu.Lock()
	clear(m.publishers)
	m.publisherCreated.Reset()
	m.publisherConnected.Reset()
	m.reconnectInterval = initialReconnectInterval
	m.mu.Unlock()

	m.notifyClientsReconnected()
}

func (m *janusSFU) notifyClientsReconnected() {
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

func (m *janusSFU) scheduleReconnect(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconnectTimer.Reset(m.reconnectInterval)
	if err == nil {
		m.logger.Printf("Connection to Janus gateway was interrupted, reconnecting in %s", m.reconnectInterval)
	} else {
		m.logger.Printf("Reconnect to Janus gateway failed (%s), reconnecting in %s", err, m.reconnectInterval)
	}

	m.reconnectInterval = min(m.reconnectInterval*2, maxReconnectInterval)
}

func (m *janusSFU) ConnectionInterrupted() {
	m.scheduleReconnect(nil)
	m.notifyOnDisconnected()
}

func (m *janusSFU) isMultistream() bool {
	return m.version >= 1000
}

func (m *janusSFU) hasRemotePublisher() bool {
	return m.version >= 1100
}

func (m *janusSFU) Start(ctx context.Context) error {
	if m.url == "" {
		if err := m.reconnect(ctx); err != nil {
			return err
		}
	}
	info, err := m.gw.Info(ctx)
	if err != nil {
		return err
	}

	m.logger.Printf("Connected to %s %s by %s", info.Name, info.VersionString, info.Author)
	m.version = info.Version

	if plugin, found := info.Plugins[pluginVideoRoom]; found {
		m.logger.Printf("Found %s %s by %s", plugin.Name, plugin.VersionString, plugin.Author)
	} else {
		return fmt.Errorf("plugin %s is not supported", pluginVideoRoom)
	}

	if plugin, found := info.Events[eventWebsocket]; found {
		if !info.EventHandlers {
			m.logger.Printf("Found %s %s by %s but event handlers are disabled, realtime usage will not be available", plugin.Name, plugin.VersionString, plugin.Author)
		} else {
			m.logger.Printf("Found %s %s by %s", plugin.Name, plugin.VersionString, plugin.Author)
		}
	} else {
		m.logger.Printf("Plugin %s not found, realtime usage will not be available", eventWebsocket)
	}

	m.logger.Printf("Used dependencies: %+v", info.Dependencies)
	if !info.DataChannels {
		return errors.New("data channels are not supported")
	}

	m.logger.Println("Data channels are supported")
	if !info.FullTrickle {
		m.logger.Println("WARNING: Full-Trickle is NOT enabled in Janus!")
	} else {
		m.logger.Println("Full-Trickle is enabled")
	}

	if m.session, err = m.gw.Create(ctx); err != nil {
		m.disconnect()
		return err
	}
	m.logger.Println("Created Janus session", m.session.Id)
	m.connectedSince = time.Now()

	if m.handle, err = m.session.Attach(ctx, pluginVideoRoom); err != nil {
		m.disconnect()
		return err
	}
	m.logger.Println("Created Janus handle", m.handle.Id)

	m.info.Store(info)

	go m.run()

	m.notifyOnConnected()
	return nil
}

func (m *janusSFU) registerClient(client clientInterface) {
	m.muClients.Lock()
	defer m.muClients.Unlock()

	m.clients[client.Handle()] = client
}

func (m *janusSFU) unregisterClient(client clientInterface) {
	m.muClients.Lock()
	defer m.muClients.Unlock()

	delete(m.clients, client.Handle())
}

func (m *janusSFU) run() {
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
			m.updateBandwidthStats(nil)
		case <-m.closeChan:
			break loop
		}
	}
}

func (m *janusSFU) Stop() {
	m.disconnect()
	m.reconnectTimer.Stop()
}

func (m *janusSFU) IsConnected() bool {
	return m.handle != nil
}

func (m *janusSFU) Info() *janus.InfoMsg {
	return m.info.Load()
}

func (m *janusSFU) GetServerInfoSfu() *talk.BackendServerInfoSfu {
	janus := &talk.BackendServerInfoSfuJanus{
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
				janus.VideoRoom = &talk.BackendServerInfoVideoRoom{
					Name:    plugin.Name,
					Version: plugin.VersionString,
					Author:  plugin.Author,
				}
			}
		}
	}

	sfu := &talk.BackendServerInfoSfu{
		Mode:  talk.SfuModeJanus,
		Janus: janus,
	}
	return sfu
}

func (m *janusSFU) Reload(config *goconf.ConfigFile) {
	m.settings.Reload(config)
}

func (m *janusSFU) SetOnConnected(f func()) {
	if f == nil {
		f = emptyOnConnected
	}

	m.onConnected.Store(f)
}

func (m *janusSFU) notifyOnConnected() {
	f := m.onConnected.Load().(func())
	f()
}

func (m *janusSFU) SetOnDisconnected(f func()) {
	if f == nil {
		f = emptyOnDisconnected
	}

	m.onDisconnected.Store(f)
}

func (m *janusSFU) notifyOnDisconnected() {
	f := m.onDisconnected.Load().(func())
	f()
}

type janusConnectionStats struct {
	Url        string     `json:"url"`
	Connected  bool       `json:"connected"`
	Publishers int64      `json:"publishers"`
	Clients    int64      `json:"clients"`
	Uptime     *time.Time `json:"uptime,omitempty"`
}

func (m *janusSFU) GetStats() any {
	result := janusConnectionStats{
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

func (m *janusSFU) sendKeepalive(ctx context.Context) {
	if _, err := m.session.KeepAlive(ctx); err != nil {
		m.logger.Println("Could not send keepalive request", err)
		if e, ok := err.(*janus.ErrorMsg); ok {
			switch e.Err.Code {
			case janus.JANUS_ERROR_SESSION_NOT_FOUND:
				m.scheduleReconnect(err)
			}
		}
	}
}

func (m *janusSFU) SubscriberConnected(id string, publisher api.PublicSessionId, streamType sfu.StreamType) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p, found := m.publishers[sfu.GetStreamId(publisher, streamType)]; found {
		p.stats.AddSubscriber(id)
	}
}

func (m *janusSFU) SubscriberDisconnected(id string, publisher api.PublicSessionId, streamType sfu.StreamType) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p, found := m.publishers[sfu.GetStreamId(publisher, streamType)]; found {
		p.stats.RemoveSubscriber(id)
	}
}

func (m *janusSFU) createPublisherRoom(ctx context.Context, handle *janus.Handle, id api.PublicSessionId, streamType sfu.StreamType, settings sfu.NewPublisherSettings) (uint64, api.Bandwidth, error) {
	create_msg := api.StringMap{
		"request":     "create",
		"description": sfu.GetStreamId(id, streamType),
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
	var maxBitrate api.Bandwidth
	if streamType == sfu.StreamTypeScreen {
		maxBitrate = m.settings.MaxScreenBitrate()
	} else {
		maxBitrate = m.settings.MaxStreamBitrate()
	}
	bitrate := settings.Bitrate
	if bitrate <= 0 {
		bitrate = maxBitrate
	} else if maxBitrate > 0 {
		bitrate = min(bitrate, maxBitrate)
	}
	if bitrate > 0 {
		create_msg["bitrate"] = bitrate.Bits()
	}
	create_response, err := handle.Request(ctx, create_msg)
	if err != nil {
		if _, err2 := handle.Detach(ctx); err2 != nil {
			m.logger.Printf("Error detaching handle %d: %s", handle.Id, err2)
		}
		return 0, 0, err
	}

	roomId := getPluginIntValue(m.logger, create_response.PluginData, pluginVideoRoom, "room")
	if roomId == 0 {
		if _, err := handle.Detach(ctx); err != nil {
			m.logger.Printf("Error detaching handle %d: %s", handle.Id, err)
		}
		return 0, 0, fmt.Errorf("no room id received: %+v", create_response)
	}

	m.logger.Println("Created room", roomId, create_response.PluginData)
	return roomId, bitrate, nil
}

func (m *janusSFU) getOrCreatePublisherHandle(ctx context.Context, id api.PublicSessionId, streamType sfu.StreamType, settings sfu.NewPublisherSettings) (*janus.Handle, uint64, uint64, api.Bandwidth, error) {
	session := m.session
	if session == nil {
		return nil, 0, 0, 0, sfu.ErrNotConnected
	}
	handle, err := session.Attach(ctx, pluginVideoRoom)
	if err != nil {
		return nil, 0, 0, 0, err
	}

	m.logger.Printf("Attached %s as publisher %d to plugin %s in session %d", streamType, handle.Id, pluginVideoRoom, session.Id)

	roomId, bitrate, err := m.createPublisherRoom(ctx, handle, id, streamType, settings)
	if err != nil {
		if _, err2 := handle.Detach(ctx); err2 != nil {
			m.logger.Printf("Error detaching handle %d: %s", handle.Id, err2)
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
			m.logger.Printf("Error detaching handle %d: %s", handle.Id, err2)
		}
		return nil, 0, 0, 0, err
	}

	return handle, response.Session, roomId, bitrate, nil
}

func (m *janusSFU) NewPublisher(ctx context.Context, listener sfu.Listener, id api.PublicSessionId, sid string, streamType sfu.StreamType, settings sfu.NewPublisherSettings, initiator sfu.Initiator) (sfu.Publisher, error) {
	if _, found := streamTypeUserIds[streamType]; !found {
		return nil, fmt.Errorf("unsupported stream type %s", streamType)
	}

	handle, session, roomId, maxBitrate, err := m.getOrCreatePublisherHandle(ctx, id, streamType, settings)
	if err != nil {
		return nil, err
	}

	client := &janusPublisher{
		janusClient: janusClient{
			logger:   m.logger,
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
		sdpReady: internal.NewCloser(),
		id:       id,
		settings: settings,
	}
	client.handle.Store(handle)
	client.handleId.Store(handle.Id)
	client.janusClient.handleEvent = client.handleEvent
	client.janusClient.handleHangup = client.handleHangup
	client.janusClient.handleDetached = client.handleDetached
	client.janusClient.handleConnected = client.handleConnected
	client.janusClient.handleSlowLink = client.handleSlowLink
	client.janusClient.handleMedia = client.handleMedia

	m.registerClient(client)
	m.logger.Printf("Publisher %s is using handle %d", client.id, handle.Id)
	go client.run(handle, client.closeChan)
	m.mu.Lock()
	m.publishers[sfu.GetStreamId(id, streamType)] = client
	m.publisherCreated.Notify(string(sfu.GetStreamId(id, streamType)))
	m.mu.Unlock()
	sfuinternal.StatsPublishersCurrent.WithLabelValues(string(streamType)).Inc()
	sfuinternal.StatsPublishersTotal.WithLabelValues(string(streamType)).Inc()
	return client, nil
}

func (m *janusSFU) getPublisher(ctx context.Context, publisher api.PublicSessionId, streamType sfu.StreamType) (*janusPublisher, error) {
	// Do the direct check immediately as this should be the normal case.
	key := sfu.GetStreamId(publisher, streamType)
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

func (m *janusSFU) getOrCreateSubscriberHandle(ctx context.Context, publisher api.PublicSessionId, streamType sfu.StreamType) (*janus.Handle, *janusPublisher, error) {
	var pub *janusPublisher
	var err error
	if pub, err = m.getPublisher(ctx, publisher, streamType); err != nil {
		return nil, nil, err
	}

	session := m.session
	if session == nil {
		return nil, nil, sfu.ErrNotConnected
	}

	handle, err := session.Attach(ctx, pluginVideoRoom)
	if err != nil {
		return nil, nil, err
	}

	m.logger.Printf("Attached subscriber to room %d of publisher %s in plugin %s in session %d as %d", pub.roomId, publisher, pluginVideoRoom, session.Id, handle.Id)
	return handle, pub, nil
}

func (m *janusSFU) NewSubscriber(ctx context.Context, listener sfu.Listener, publisher api.PublicSessionId, streamType sfu.StreamType, initiator sfu.Initiator) (sfu.Subscriber, error) {
	if _, found := streamTypeUserIds[streamType]; !found {
		return nil, fmt.Errorf("unsupported stream type %s", streamType)
	}

	handle, pub, err := m.getOrCreateSubscriberHandle(ctx, publisher, streamType)
	if err != nil {
		return nil, err
	}

	client := &janusSubscriber{
		janusClient: janusClient{
			logger:   m.logger,
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
	client.janusClient.handleEvent = client.handleEvent
	client.janusClient.handleHangup = client.handleHangup
	client.janusClient.handleDetached = client.handleDetached
	client.janusClient.handleConnected = client.handleConnected
	client.janusClient.handleSlowLink = client.handleSlowLink
	client.janusClient.handleMedia = client.handleMedia
	m.registerClient(client)
	go client.run(handle, client.closeChan)
	m.stats.IncSubscriber(streamType)
	sfuinternal.StatsSubscribersTotal.WithLabelValues(string(streamType)).Inc()
	return client, nil
}

func (m *janusSFU) getOrCreateRemotePublisher(ctx context.Context, controller sfu.RemotePublisherController, streamType sfu.StreamType, settings sfu.NewPublisherSettings) (*janusRemotePublisher, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pub, found := m.remotePublishers[sfu.GetStreamId(controller.PublisherId(), streamType)]
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
		return nil, sfu.ErrNotConnected
	}

	handle, err := session.Attach(ctx, pluginVideoRoom)
	if err != nil {
		return nil, err
	}

	roomId, maxBitrate, err := m.createPublisherRoom(ctx, handle, controller.PublisherId(), streamType, settings)
	if err != nil {
		if _, err2 := handle.Detach(ctx); err2 != nil {
			m.logger.Printf("Error detaching handle %d: %s", handle.Id, err2)
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
			m.logger.Printf("Error detaching handle %d: %s", handle.Id, err2)
		}
		return nil, err
	}

	id := getPluginIntValue(m.logger, response.PluginData, pluginVideoRoom, "id")
	port := getPluginIntValue(m.logger, response.PluginData, pluginVideoRoom, "port")
	rtcp_port := getPluginIntValue(m.logger, response.PluginData, pluginVideoRoom, "rtcp_port")

	pub = &janusRemotePublisher{
		janusPublisher: janusPublisher{
			janusClient: janusClient{
				logger: m.logger,
				mcu:    m,

				id:         id,
				session:    response.Session,
				roomId:     roomId,
				sid:        strconv.FormatUint(handle.Id, 10),
				streamType: streamType,
				maxBitrate: maxBitrate,

				closeChan: make(chan struct{}, 1),
				deferred:  make(chan func(), 64),
			},

			sdpReady: internal.NewCloser(),
			id:       controller.PublisherId(),
			settings: settings,
		},

		controller: controller,

		port:     int(port),
		rtcpPort: int(rtcp_port),
	}
	pub.handle.Store(handle)
	pub.handleId.Store(handle.Id)
	pub.janusClient.handleEvent = pub.handleEvent
	pub.janusClient.handleHangup = pub.handleHangup
	pub.janusClient.handleDetached = pub.handleDetached
	pub.janusClient.handleConnected = pub.handleConnected
	pub.janusClient.handleSlowLink = pub.handleSlowLink
	pub.janusClient.handleMedia = pub.handleMedia

	if err := controller.StartPublishing(ctx, pub); err != nil {
		go pub.Close(context.Background())
		return nil, err
	}

	m.remotePublishers[sfu.GetStreamId(controller.PublisherId(), streamType)] = pub

	return pub, nil
}

func (m *janusSFU) NewRemotePublisher(ctx context.Context, listener sfu.Listener, controller sfu.RemotePublisherController, streamType sfu.StreamType) (sfu.RemotePublisher, error) {
	if _, found := streamTypeUserIds[streamType]; !found {
		return nil, fmt.Errorf("unsupported stream type %s", streamType)
	}

	if !m.hasRemotePublisher() {
		return nil, ErrRemoteStreamsNotSupported
	}

	pub, err := m.getOrCreateRemotePublisher(ctx, controller, streamType, sfu.NewPublisherSettings{})
	if err != nil {
		return nil, err
	}

	pub.addRef()
	return pub, nil
}

func (m *janusSFU) NewRemoteSubscriber(ctx context.Context, listener sfu.Listener, publisher sfu.RemotePublisher) (sfu.RemoteSubscriber, error) {
	pub, ok := publisher.(*janusRemotePublisher)
	if !ok {
		return nil, errors.New("unsupported remote publisher")
	}

	session := m.session
	if session == nil {
		return nil, sfu.ErrNotConnected
	}

	handle, err := session.Attach(ctx, pluginVideoRoom)
	if err != nil {
		return nil, err
	}

	m.logger.Printf("Attached subscriber to room %d of publisher %s in plugin %s in session %d as %d", pub.roomId, pub.id, pluginVideoRoom, session.Id, handle.Id)

	client := &janusRemoteSubscriber{
		janusSubscriber: janusSubscriber{
			janusClient: janusClient{
				logger:   m.logger,
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
	client.janusClient.handleEvent = client.handleEvent
	client.janusClient.handleHangup = client.handleHangup
	client.janusClient.handleDetached = client.handleDetached
	client.janusClient.handleConnected = client.handleConnected
	client.janusClient.handleSlowLink = client.handleSlowLink
	client.janusClient.handleMedia = client.handleMedia
	m.registerClient(client)
	go client.run(handle, client.closeChan)
	m.stats.IncSubscriber(publisher.StreamType())
	sfuinternal.StatsSubscribersTotal.WithLabelValues(string(publisher.StreamType())).Inc()
	return client, nil
}

func (m *janusSFU) UpdateBandwidth(handle uint64, media string, sent api.Bandwidth, received api.Bandwidth) {
	m.muClients.RLock()
	defer m.muClients.RUnlock()

	client, found := m.clients[handle]
	if !found {
		return
	}

	client.UpdateBandwidth(media, sent, received)
}
