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
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"github.com/notedit/janus-go"
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

	streamTypeVideo  = "video"
	streamTypeScreen = "screen"
)

var (
	streamTypeUserIds = map[string]uint64{
		streamTypeVideo:  videoPublisherUserId,
		streamTypeScreen: screenPublisherUserId,
	}
)

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
	NotifyReconnected()
}

type mcuJanus struct {
	// 64-bit members that are accessed atomically must be 64-bit aligned.
	clientId uint64

	url string
	mu  sync.Mutex

	maxStreamBitrate int
	maxScreenBitrate int
	mcuTimeout       time.Duration

	gw      *JanusGateway
	session *JanusSession
	handle  *JanusHandle

	closeChan chan bool

	muClients sync.Mutex
	clients   map[clientInterface]bool

	publishers         map[string]*mcuJanusPublisher
	publisherCreated   Notifier
	publisherConnected Notifier

	reconnectTimer    *time.Timer
	reconnectInterval time.Duration

	connectedSince time.Time
	onConnected    atomic.Value
	onDisconnected atomic.Value
}

func emptyOnConnected()    {}
func emptyOnDisconnected() {}

func NewMcuJanus(url string, config *goconf.ConfigFile) (Mcu, error) {
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
		url:              url,
		maxStreamBitrate: maxStreamBitrate,
		maxScreenBitrate: maxScreenBitrate,
		mcuTimeout:       mcuTimeout,
		closeChan:        make(chan bool, 1),
		clients:          make(map[clientInterface]bool),

		publishers: make(map[string]*mcuJanusPublisher),

		reconnectInterval: initialReconnectInterval,
	}
	mcu.onConnected.Store(emptyOnConnected)
	mcu.onDisconnected.Store(emptyOnDisconnected)

	mcu.reconnectTimer = time.AfterFunc(mcu.reconnectInterval, mcu.doReconnect)
	mcu.reconnectTimer.Stop()
	if err := mcu.reconnect(); err != nil {
		return nil, err
	}
	return mcu, nil
}

func (m *mcuJanus) disconnect() {
	if m.handle != nil {
		if _, err := m.handle.Detach(context.TODO()); err != nil {
			log.Printf("Error detaching handle %d: %s", m.handle.Id, err)
		}
		m.handle = nil
	}
	if m.session != nil {
		m.closeChan <- true
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

func (m *mcuJanus) reconnect() error {
	m.disconnect()
	gw, err := NewJanusGateway(m.url, m)
	if err != nil {
		return err
	}

	m.gw = gw
	m.reconnectTimer.Stop()
	return nil
}

func (m *mcuJanus) doReconnect() {
	if err := m.reconnect(); err != nil {
		m.scheduleReconnect(err)
		return
	}
	if err := m.Start(); err != nil {
		m.scheduleReconnect(err)
		return
	}

	log.Println("Reconnection to Janus gateway successful")
	m.mu.Lock()
	m.publishers = make(map[string]*mcuJanusPublisher)
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
		log.Printf("Connection to Janus gateway was interrupted, reconnecting in %s", m.reconnectInterval)
	} else {
		log.Printf("Reconnect to Janus gateway failed (%s), reconnecting in %s", err, m.reconnectInterval)
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

func (m *mcuJanus) Start() error {
	ctx := context.TODO()
	info, err := m.gw.Info(ctx)
	if err != nil {
		return err
	}

	log.Printf("Connected to %s %s by %s", info.Name, info.VersionString, info.Author)
	plugin, found := info.Plugins[pluginVideoRoom]
	if !found {
		return fmt.Errorf("Plugin %s is not supported", pluginVideoRoom)
	}

	log.Printf("Found %s %s by %s", plugin.Name, plugin.VersionString, plugin.Author)
	if !info.DataChannels {
		return fmt.Errorf("Data channels are not supported")
	}

	log.Println("Data channels are supported")
	if !info.FullTrickle {
		log.Println("WARNING: Full-Trickle is NOT enabled in Janus!")
	} else {
		log.Println("Full-Trickle is enabled")
	}
	log.Printf("Maximum bandwidth %d bits/sec per publishing stream", m.maxStreamBitrate)
	log.Printf("Maximum bandwidth %d bits/sec per screensharing stream", m.maxScreenBitrate)

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
			m.sendKeepalive()
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

func (m *mcuJanus) sendKeepalive() {
	ctx := context.TODO()
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

type mcuJanusClient struct {
	mcu      *mcuJanus
	listener McuListener
	mu       sync.Mutex // nolint

	id         uint64
	session    uint64
	roomId     uint64
	sid        string
	streamType string

	handle    *JanusHandle
	handleId  uint64
	closeChan chan bool
	deferred  chan func()

	handleEvent     func(event *janus.EventMsg)
	handleHangup    func(event *janus.HangupMsg)
	handleDetached  func(event *janus.DetachedMsg)
	handleConnected func(event *janus.WebRTCUpMsg)
	handleSlowLink  func(event *janus.SlowLinkMsg)
	handleMedia     func(event *janus.MediaMsg)
}

func (c *mcuJanusClient) Id() string {
	return strconv.FormatUint(c.id, 10)
}

func (c *mcuJanusClient) Sid() string {
	return c.sid
}

func (c *mcuJanusClient) StreamType() string {
	return c.streamType
}

func (c *mcuJanusClient) Close(ctx context.Context) {
}

func (c *mcuJanusClient) SendMessage(ctx context.Context, message *MessageClientMessage, data *MessageClientMessageData, callback func(error, map[string]interface{})) {
}

func (c *mcuJanusClient) closeClient(ctx context.Context) bool {
	if handle := c.handle; handle != nil {
		c.handle = nil
		c.closeChan <- true
		if _, err := handle.Detach(ctx); err != nil {
			if e, ok := err.(*janus.ErrorMsg); !ok || e.Err.Code != JANUS_ERROR_HANDLE_NOT_FOUND {
				log.Println("Could not detach client", handle.Id, err)
			}
		}
		return true
	}

	return false
}

func (c *mcuJanusClient) run(handle *JanusHandle, closeChan chan bool) {
loop:
	for {
		select {
		case msg := <-handle.Events:
			switch t := msg.(type) {
			case *janus.EventMsg:
				c.handleEvent(t)
			case *janus.HangupMsg:
				c.handleHangup(t)
			case *janus.DetachedMsg:
				c.handleDetached(t)
			case *janus.MediaMsg:
				c.handleMedia(t)
			case *janus.WebRTCUpMsg:
				c.handleConnected(t)
			case *janus.SlowLinkMsg:
				c.handleSlowLink(t)
			case *TrickleMsg:
				c.handleTrickle(t)
			default:
				log.Println("Received unsupported event type", msg, reflect.TypeOf(msg))
			}
		case f := <-c.deferred:
			f()
		case <-closeChan:
			break loop
		}
	}
}

func (c *mcuJanusClient) sendOffer(ctx context.Context, offer map[string]interface{}, callback func(error, map[string]interface{})) {
	handle := c.handle
	if handle == nil {
		callback(ErrNotConnected, nil)
		return
	}

	configure_msg := map[string]interface{}{
		"request": "configure",
		"audio":   true,
		"video":   true,
		"data":    true,
	}
	answer_msg, err := handle.Message(ctx, configure_msg, offer)
	if err != nil {
		callback(err, nil)
		return
	}

	callback(nil, answer_msg.Jsep)
}

func (c *mcuJanusClient) sendAnswer(ctx context.Context, answer map[string]interface{}, callback func(error, map[string]interface{})) {
	handle := c.handle
	if handle == nil {
		callback(ErrNotConnected, nil)
		return
	}

	start_msg := map[string]interface{}{
		"request": "start",
		"room":    c.roomId,
	}
	start_response, err := handle.Message(ctx, start_msg, answer)
	if err != nil {
		callback(err, nil)
		return
	}
	log.Println("Started listener", start_response)
	callback(nil, nil)
}

func (c *mcuJanusClient) sendCandidate(ctx context.Context, candidate interface{}, callback func(error, map[string]interface{})) {
	handle := c.handle
	if handle == nil {
		callback(ErrNotConnected, nil)
		return
	}

	if _, err := handle.Trickle(ctx, candidate); err != nil {
		callback(err, nil)
		return
	}
	callback(nil, nil)
}

func (c *mcuJanusClient) handleTrickle(event *TrickleMsg) {
	if event.Candidate.Completed {
		c.listener.OnIceCompleted(c)
	} else {
		c.listener.OnIceCandidate(c, event.Candidate)
	}
}

func (c *mcuJanusClient) selectStream(ctx context.Context, stream *streamSelection, callback func(error, map[string]interface{})) {
	handle := c.handle
	if handle == nil {
		callback(ErrNotConnected, nil)
		return
	}

	if stream == nil || !stream.HasValues() {
		callback(nil, nil)
		return
	}

	configure_msg := map[string]interface{}{
		"request": "configure",
	}
	if stream != nil {
		stream.AddToMessage(configure_msg)
	}
	_, err := handle.Message(ctx, configure_msg, nil)
	if err != nil {
		callback(err, nil)
		return
	}

	callback(nil, nil)
}

type publisherStatsCounter struct {
	mu sync.Mutex

	streamTypes map[string]bool
	subscribers map[string]bool
}

func (c *publisherStatsCounter) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	count := len(c.subscribers)
	for streamType := range c.streamTypes {
		statsMcuPublisherStreamTypesCurrent.WithLabelValues(streamType).Dec()
		statsMcuSubscriberStreamTypesCurrent.WithLabelValues(streamType).Sub(float64(count))
	}
	c.streamTypes = nil
	c.subscribers = nil
}

func (c *publisherStatsCounter) EnableStream(streamType string, enable bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if enable == c.streamTypes[streamType] {
		return
	}

	if enable {
		if c.streamTypes == nil {
			c.streamTypes = make(map[string]bool)
		}
		c.streamTypes[streamType] = true
		statsMcuPublisherStreamTypesCurrent.WithLabelValues(streamType).Inc()
		statsMcuSubscriberStreamTypesCurrent.WithLabelValues(streamType).Add(float64(len(c.subscribers)))
	} else {
		delete(c.streamTypes, streamType)
		statsMcuPublisherStreamTypesCurrent.WithLabelValues(streamType).Dec()
		statsMcuSubscriberStreamTypesCurrent.WithLabelValues(streamType).Sub(float64(len(c.subscribers)))
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
		statsMcuSubscriberStreamTypesCurrent.WithLabelValues(streamType).Inc()
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
		statsMcuSubscriberStreamTypesCurrent.WithLabelValues(streamType).Dec()
	}
}

type mcuJanusPublisher struct {
	mcuJanusClient

	id         string
	bitrate    int
	mediaTypes MediaType
	stats      publisherStatsCounter
}

func (m *mcuJanus) SubscriberConnected(id string, publisher string, streamType string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p, found := m.publishers[publisher+"|"+streamType]; found {
		p.stats.AddSubscriber(id)
	}
}

func (m *mcuJanus) SubscriberDisconnected(id string, publisher string, streamType string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if p, found := m.publishers[publisher+"|"+streamType]; found {
		p.stats.RemoveSubscriber(id)
	}
}

func min(a, b int) int {
	if a <= b {
		return a
	}

	return b
}

func (m *mcuJanus) getOrCreatePublisherHandle(ctx context.Context, id string, streamType string, bitrate int) (*JanusHandle, uint64, uint64, error) {
	session := m.session
	if session == nil {
		return nil, 0, 0, ErrNotConnected
	}
	handle, err := session.Attach(ctx, pluginVideoRoom)
	if err != nil {
		return nil, 0, 0, err
	}

	log.Printf("Attached %s as publisher %d to plugin %s in session %d", streamType, handle.Id, pluginVideoRoom, session.Id)
	create_msg := map[string]interface{}{
		"request":     "create",
		"description": id + "|" + streamType,
		// We publish every stream in its own Janus room.
		"publishers": 1,
		// Do not use the video-orientation RTP extension as it breaks video
		// orientation changes in Firefox.
		"videoorient_ext": false,
	}
	var maxBitrate int
	if streamType == streamTypeScreen {
		maxBitrate = m.maxScreenBitrate
	} else {
		maxBitrate = m.maxStreamBitrate
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
			log.Printf("Error detaching handle %d: %s", handle.Id, err2)
		}
		return nil, 0, 0, err
	}

	roomId := getPluginIntValue(create_response.PluginData, pluginVideoRoom, "room")
	if roomId == 0 {
		if _, err := handle.Detach(ctx); err != nil {
			log.Printf("Error detaching handle %d: %s", handle.Id, err)
		}
		return nil, 0, 0, fmt.Errorf("No room id received: %+v", create_response)
	}

	log.Println("Created room", roomId, create_response.PluginData)

	msg := map[string]interface{}{
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
		return nil, 0, 0, err
	}

	return handle, response.Session, roomId, nil
}

func (m *mcuJanus) NewPublisher(ctx context.Context, listener McuListener, id string, sid string, streamType string, bitrate int, mediaTypes MediaType, initiator McuInitiator) (McuPublisher, error) {
	if _, found := streamTypeUserIds[streamType]; !found {
		return nil, fmt.Errorf("Unsupported stream type %s", streamType)
	}

	handle, session, roomId, err := m.getOrCreatePublisherHandle(ctx, id, streamType, bitrate)
	if err != nil {
		return nil, err
	}

	client := &mcuJanusPublisher{
		mcuJanusClient: mcuJanusClient{
			mcu:      m,
			listener: listener,

			id:         atomic.AddUint64(&m.clientId, 1),
			session:    session,
			roomId:     roomId,
			sid:        sid,
			streamType: streamType,

			handle:    handle,
			handleId:  handle.Id,
			closeChan: make(chan bool, 1),
			deferred:  make(chan func(), 64),
		},
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

	m.registerClient(client)
	log.Printf("Publisher %s is using handle %d", client.id, client.handleId)
	go client.run(handle, client.closeChan)
	m.mu.Lock()
	m.publishers[id+"|"+streamType] = client
	m.publisherCreated.Notify(id + "|" + streamType)
	m.mu.Unlock()
	statsPublishersCurrent.WithLabelValues(streamType).Inc()
	statsPublishersTotal.WithLabelValues(streamType).Inc()
	return client, nil
}

func (p *mcuJanusPublisher) handleEvent(event *janus.EventMsg) {
	if videoroom := getPluginStringValue(event.Plugindata, pluginVideoRoom, "videoroom"); videoroom != "" {
		ctx := context.TODO()
		switch videoroom {
		case "destroyed":
			log.Printf("Publisher %d: associated room has been destroyed, closing", p.handleId)
			go p.Close(ctx)
		case "slow_link":
			// Ignore, processed through "handleSlowLink" in the general events.
		default:
			log.Printf("Unsupported videoroom publisher event in %d: %+v", p.handleId, event)
		}
	} else {
		log.Printf("Unsupported publisher event in %d: %+v", p.handleId, event)
	}
}

func (p *mcuJanusPublisher) handleHangup(event *janus.HangupMsg) {
	log.Printf("Publisher %d received hangup (%s), closing", p.handleId, event.Reason)
	go p.Close(context.Background())
}

func (p *mcuJanusPublisher) handleDetached(event *janus.DetachedMsg) {
	log.Printf("Publisher %d received detached, closing", p.handleId)
	go p.Close(context.Background())
}

func (p *mcuJanusPublisher) handleConnected(event *janus.WebRTCUpMsg) {
	log.Printf("Publisher %d received connected", p.handleId)
	p.mcu.publisherConnected.Notify(p.id + "|" + p.streamType)
}

func (p *mcuJanusPublisher) handleSlowLink(event *janus.SlowLinkMsg) {
	if event.Uplink {
		log.Printf("Publisher %s (%d) is reporting %d lost packets on the uplink (Janus -> client)", p.listener.PublicId(), p.handleId, event.Lost)
	} else {
		log.Printf("Publisher %s (%d) is reporting %d lost packets on the downlink (client -> Janus)", p.listener.PublicId(), p.handleId, event.Lost)
	}
}

func (p *mcuJanusPublisher) handleMedia(event *janus.MediaMsg) {
	mediaType := event.Type
	if mediaType == "video" && p.streamType == "screen" {
		// We want to differentiate between audio, video and screensharing
		mediaType = p.streamType
	}

	p.stats.EnableStream(mediaType, event.Receiving)
}

func (p *mcuJanusPublisher) HasMedia(mt MediaType) bool {
	return (p.mediaTypes & mt) == mt
}

func (p *mcuJanusPublisher) SetMedia(mt MediaType) {
	p.mediaTypes = mt
}

func (p *mcuJanusPublisher) NotifyReconnected() {
	ctx := context.TODO()
	handle, session, roomId, err := p.mcu.getOrCreatePublisherHandle(ctx, p.id, p.streamType, p.bitrate)
	if err != nil {
		log.Printf("Could not reconnect publisher %s: %s", p.id, err)
		// TODO(jojo): Retry
		return
	}

	p.handle = handle
	p.handleId = handle.Id
	p.session = session
	p.roomId = roomId

	log.Printf("Publisher %s reconnected on handle %d", p.id, p.handleId)
}

func (p *mcuJanusPublisher) Close(ctx context.Context) {
	notify := false
	p.mu.Lock()
	if handle := p.handle; handle != nil && p.roomId != 0 {
		destroy_msg := map[string]interface{}{
			"request": "destroy",
			"room":    p.roomId,
		}
		if _, err := handle.Request(ctx, destroy_msg); err != nil {
			log.Printf("Error destroying room %d: %s", p.roomId, err)
		} else {
			log.Printf("Room %d destroyed", p.roomId)
		}
		p.mcu.mu.Lock()
		delete(p.mcu.publishers, p.id+"|"+p.streamType)
		p.mcu.mu.Unlock()
		p.roomId = 0
		notify = true
	}
	p.closeClient(ctx)
	p.mu.Unlock()

	p.stats.Reset()

	if notify {
		statsPublishersCurrent.WithLabelValues(p.streamType).Dec()
		p.mcu.unregisterClient(p)
		p.listener.PublisherClosed(p)
	}
	p.mcuJanusClient.Close(ctx)
}

func (p *mcuJanusPublisher) SendMessage(ctx context.Context, message *MessageClientMessage, data *MessageClientMessageData, callback func(error, map[string]interface{})) {
	statsMcuMessagesTotal.WithLabelValues(data.Type).Inc()
	jsep_msg := data.Payload
	switch data.Type {
	case "offer":
		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			// TODO Tear down previous publisher and get a new one if sid does
			// not match?
			p.sendOffer(msgctx, jsep_msg, callback)
		}
	case "candidate":
		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			if data.Sid == "" || data.Sid == p.Sid() {
				p.sendCandidate(msgctx, jsep_msg["candidate"], callback)
			} else {
				go callback(fmt.Errorf("Candidate message sid (%s) does not match publisher sid (%s)", data.Sid, p.Sid()), nil)
			}
		}
	case "endOfCandidates":
		// Ignore
	default:
		go callback(fmt.Errorf("Unsupported message type: %s", data.Type), nil)
	}
}

type mcuJanusSubscriber struct {
	mcuJanusClient

	publisher string
}

func (m *mcuJanus) getPublisher(ctx context.Context, publisher string, streamType string) (*mcuJanusPublisher, error) {
	// Do the direct check immediately as this should be the normal case.
	key := publisher + "|" + streamType
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

func (m *mcuJanus) getOrCreateSubscriberHandle(ctx context.Context, publisher string, streamType string) (*JanusHandle, *mcuJanusPublisher, error) {
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

func (m *mcuJanus) NewSubscriber(ctx context.Context, listener McuListener, publisher string, streamType string) (McuSubscriber, error) {
	if _, found := streamTypeUserIds[streamType]; !found {
		return nil, fmt.Errorf("Unsupported stream type %s", streamType)
	}

	handle, pub, err := m.getOrCreateSubscriberHandle(ctx, publisher, streamType)
	if err != nil {
		return nil, err
	}

	client := &mcuJanusSubscriber{
		mcuJanusClient: mcuJanusClient{
			mcu:      m,
			listener: listener,

			id:         atomic.AddUint64(&m.clientId, 1),
			roomId:     pub.roomId,
			sid:        strconv.FormatUint(handle.Id, 10),
			streamType: streamType,

			handle:    handle,
			handleId:  handle.Id,
			closeChan: make(chan bool, 1),
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
	m.registerClient(client)
	go client.run(handle, client.closeChan)
	statsSubscribersCurrent.WithLabelValues(streamType).Inc()
	statsSubscribersTotal.WithLabelValues(streamType).Inc()
	return client, nil
}

func (p *mcuJanusSubscriber) Publisher() string {
	return p.publisher
}

func (p *mcuJanusSubscriber) handleEvent(event *janus.EventMsg) {
	if videoroom := getPluginStringValue(event.Plugindata, pluginVideoRoom, "videoroom"); videoroom != "" {
		ctx := context.TODO()
		switch videoroom {
		case "destroyed":
			log.Printf("Subscriber %d: associated room has been destroyed, closing", p.handleId)
			go p.Close(ctx)
		case "event":
			// Handle renegotiations, but ignore other events like selected
			// substream / temporal layer.
			if getPluginStringValue(event.Plugindata, pluginVideoRoom, "configured") == "ok" &&
				event.Jsep != nil && event.Jsep["type"] == "offer" && event.Jsep["sdp"] != nil {
				p.listener.OnUpdateOffer(p, event.Jsep)
			}
		case "slow_link":
			// Ignore, processed through "handleSlowLink" in the general events.
		default:
			log.Printf("Unsupported videoroom event %s for subscriber %d: %+v", videoroom, p.handleId, event)
		}
	} else {
		log.Printf("Unsupported event for subscriber %d: %+v", p.handleId, event)
	}
}

func (p *mcuJanusSubscriber) handleHangup(event *janus.HangupMsg) {
	log.Printf("Subscriber %d received hangup (%s), closing", p.handleId, event.Reason)
	go p.Close(context.Background())
}

func (p *mcuJanusSubscriber) handleDetached(event *janus.DetachedMsg) {
	log.Printf("Subscriber %d received detached, closing", p.handleId)
	go p.Close(context.Background())
}

func (p *mcuJanusSubscriber) handleConnected(event *janus.WebRTCUpMsg) {
	log.Printf("Subscriber %d received connected", p.handleId)
	p.mcu.SubscriberConnected(p.Id(), p.publisher, p.streamType)
}

func (p *mcuJanusSubscriber) handleSlowLink(event *janus.SlowLinkMsg) {
	if event.Uplink {
		log.Printf("Subscriber %s (%d) is reporting %d lost packets on the uplink (Janus -> client)", p.listener.PublicId(), p.handleId, event.Lost)
	} else {
		log.Printf("Subscriber %s (%d) is reporting %d lost packets on the downlink (client -> Janus)", p.listener.PublicId(), p.handleId, event.Lost)
	}
}

func (p *mcuJanusSubscriber) handleMedia(event *janus.MediaMsg) {
	// Only triggered for publishers
}

func (p *mcuJanusSubscriber) NotifyReconnected() {
	ctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
	defer cancel()
	handle, pub, err := p.mcu.getOrCreateSubscriberHandle(ctx, p.publisher, p.streamType)
	if err != nil {
		// TODO(jojo): Retry?
		log.Printf("Could not reconnect subscriber for publisher %s: %s", p.publisher, err)
		p.Close(context.Background())
		return
	}

	p.handle = handle
	p.handleId = handle.Id
	p.roomId = pub.roomId
	p.sid = strconv.FormatUint(handle.Id, 10)
	p.listener.SubscriberSidUpdated(p)
	log.Printf("Subscriber %d for publisher %s reconnected on handle %d", p.id, p.publisher, p.handleId)
}

func (p *mcuJanusSubscriber) Close(ctx context.Context) {
	p.mu.Lock()
	closed := p.closeClient(ctx)
	p.mu.Unlock()

	if closed {
		p.mcu.SubscriberDisconnected(p.Id(), p.publisher, p.streamType)
		statsSubscribersCurrent.WithLabelValues(p.streamType).Dec()
	}
	p.mcu.unregisterClient(p)
	p.listener.SubscriberClosed(p)
	p.mcuJanusClient.Close(ctx)
}

func (p *mcuJanusSubscriber) joinRoom(ctx context.Context, stream *streamSelection, callback func(error, map[string]interface{})) {
	handle := p.handle
	if handle == nil {
		callback(ErrNotConnected, nil)
		return
	}

	waiter := p.mcu.publisherConnected.NewWaiter(p.publisher + "|" + p.streamType)
	defer p.mcu.publisherConnected.Release(waiter)

	loggedNotPublishingYet := false
retry:
	join_msg := map[string]interface{}{
		"request": "join",
		"ptype":   "subscriber",
		"room":    p.roomId,
		"feed":    streamTypeUserIds[p.streamType],
	}
	if stream != nil {
		stream.AddToMessage(join_msg)
	}
	join_response, err := handle.Message(ctx, join_msg, nil)
	if err != nil {
		callback(err, nil)
		return
	}

	if error_code := getPluginIntValue(join_response.Plugindata, pluginVideoRoom, "error_code"); error_code > 0 {
		switch error_code {
		case JANUS_VIDEOROOM_ERROR_ALREADY_JOINED:
			// The subscriber is already connected to the room. This can happen
			// if a client leaves a call but keeps the subscriber objects active.
			// On joining the call again, the subscriber tries to join on the
			// MCU which will fail because he is still connected.
			// To get a new Offer SDP, we have to tear down the session on the
			// MCU and join again.
			p.mu.Lock()
			p.closeClient(ctx)
			p.mu.Unlock()

			var pub *mcuJanusPublisher
			handle, pub, err = p.mcu.getOrCreateSubscriberHandle(ctx, p.publisher, p.streamType)
			if err != nil {
				// Reconnection didn't work, need to unregister/remove subscriber
				// so a new object will be created if the request is retried.
				p.mcu.unregisterClient(p)
				p.listener.SubscriberClosed(p)
				callback(fmt.Errorf("Already connected as subscriber for %s, error during re-joining: %s", p.streamType, err), nil)
				return
			}

			p.handle = handle
			p.handleId = handle.Id
			p.roomId = pub.roomId
			p.sid = strconv.FormatUint(handle.Id, 10)
			p.listener.SubscriberSidUpdated(p)
			p.closeChan = make(chan bool, 1)
			go p.run(p.handle, p.closeChan)
			log.Printf("Already connected subscriber %d for %s, leaving and re-joining on handle %d", p.id, p.streamType, p.handleId)
			goto retry
		case JANUS_VIDEOROOM_ERROR_NO_SUCH_ROOM:
			fallthrough
		case JANUS_VIDEOROOM_ERROR_NO_SUCH_FEED:
			switch error_code {
			case JANUS_VIDEOROOM_ERROR_NO_SUCH_ROOM:
				log.Printf("Publisher %s not created yet for %s, wait and retry to join room %d as subscriber", p.publisher, p.streamType, p.roomId)
			case JANUS_VIDEOROOM_ERROR_NO_SUCH_FEED:
				log.Printf("Publisher %s not sending yet for %s, wait and retry to join room %d as subscriber", p.publisher, p.streamType, p.roomId)
			}

			if !loggedNotPublishingYet {
				loggedNotPublishingYet = true
				statsWaitingForPublisherTotal.WithLabelValues(p.streamType).Inc()
			}

			if err := waiter.Wait(ctx); err != nil {
				callback(err, nil)
				return
			}
			log.Printf("Retry subscribing %s from %s", p.streamType, p.publisher)
			goto retry
		default:
			// TODO(jojo): Should we handle other errors, too?
			callback(fmt.Errorf("Error joining room as subscriber: %+v", join_response), nil)
			return
		}
	}
	//log.Println("Joined as listener", join_response)

	p.session = join_response.Session
	callback(nil, join_response.Jsep)
}

func (p *mcuJanusSubscriber) update(ctx context.Context, stream *streamSelection, callback func(error, map[string]interface{})) {
	handle := p.handle
	if handle == nil {
		callback(ErrNotConnected, nil)
		return
	}

	configure_msg := map[string]interface{}{
		"request": "configure",
		"update":  true,
	}
	if stream != nil {
		stream.AddToMessage(configure_msg)
	}
	configure_response, err := handle.Message(ctx, configure_msg, nil)
	if err != nil {
		callback(err, nil)
		return
	}

	callback(nil, configure_response.Jsep)
}

type streamSelection struct {
	substream sql.NullInt16
	temporal  sql.NullInt16
	audio     sql.NullBool
	video     sql.NullBool
}

func (s *streamSelection) HasValues() bool {
	return s.substream.Valid || s.temporal.Valid || s.audio.Valid || s.video.Valid
}

func (s *streamSelection) AddToMessage(message map[string]interface{}) {
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

func parseStreamSelection(payload map[string]interface{}) (*streamSelection, error) {
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
			return nil, fmt.Errorf("Unsupported substream value: %v", value)
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
			return nil, fmt.Errorf("Unsupported temporal value: %v", value)
		}
	}

	if value, found := payload["audio"]; found {
		switch value := value.(type) {
		case bool:
			stream.audio.Valid = true
			stream.audio.Bool = value
		default:
			return nil, fmt.Errorf("Unsupported audio value: %v", value)
		}
	}

	if value, found := payload["video"]; found {
		switch value := value.(type) {
		case bool:
			stream.video.Valid = true
			stream.video.Bool = value
		default:
			return nil, fmt.Errorf("Unsupported video value: %v", value)
		}
	}

	return &stream, nil
}

func (p *mcuJanusSubscriber) SendMessage(ctx context.Context, message *MessageClientMessage, data *MessageClientMessageData, callback func(error, map[string]interface{})) {
	statsMcuMessagesTotal.WithLabelValues(data.Type).Inc()
	jsep_msg := data.Payload
	switch data.Type {
	case "requestoffer":
		fallthrough
	case "sendoffer":
		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			stream, err := parseStreamSelection(jsep_msg)
			if err != nil {
				go callback(err, nil)
				return
			}

			if data.Sid == "" || data.Sid != p.Sid() {
				p.joinRoom(msgctx, stream, callback)
			} else {
				p.update(msgctx, stream, callback)
			}
		}
	case "answer":
		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			if data.Sid == "" || data.Sid == p.Sid() {
				p.sendAnswer(msgctx, jsep_msg, callback)
			} else {
				go callback(fmt.Errorf("Answer message sid (%s) does not match subscriber sid (%s)", data.Sid, p.Sid()), nil)
			}
		}
	case "candidate":
		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			if data.Sid == "" || data.Sid == p.Sid() {
				p.sendCandidate(msgctx, jsep_msg["candidate"], callback)
			} else {
				go callback(fmt.Errorf("Candidate message sid (%s) does not match subscriber sid (%s)", data.Sid, p.Sid()), nil)
			}
		}
	case "endOfCandidates":
		// Ignore
	case "selectStream":
		stream, err := parseStreamSelection(jsep_msg)
		if err != nil {
			go callback(err, nil)
			return
		}

		if stream == nil || !stream.HasValues() {
			// Nothing to do
			go callback(nil, nil)
			return
		}

		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			p.selectStream(msgctx, stream, callback)
		}
	default:
		// Return error asynchronously
		go callback(fmt.Errorf("Unsupported message type: %s", data.Type), nil)
	}
}
