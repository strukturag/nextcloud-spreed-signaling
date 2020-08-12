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
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"github.com/nats-io/go-nats"
	"github.com/notedit/janus-go"

	"golang.org/x/net/context"
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
	userIdToStreamType = map[uint64]string{
		videoPublisherUserId:  streamTypeVideo,
		screenPublisherUserId: streamTypeScreen,
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
			return 0, fmt.Errorf("Unsupported float64 number: %+v\n", t)
		}
		return uint64(t), nil
	case uint64:
		return t, nil
	case int64:
		if t < 0 {
			return 0, fmt.Errorf("Unsupported int64 number: %+v\n", t)
		}
		return uint64(t), nil
	case json.Number:
		r, err := t.Int64()
		if err != nil {
			return 0, err
		} else if r < 0 {
			return 0, fmt.Errorf("Unsupported JSON number: %+v\n", t)
		}
		return uint64(r), nil
	default:
		return 0, fmt.Errorf("Unknown number type: %+v\n", t)
	}
}

func getPluginIntValue(data janus.PluginData, pluginName string, key string) uint64 {
	val := getPluginValue(data, pluginName, key)
	if val == nil {
		return 0
	}

	result, err := convertIntValue(val)
	if err != nil {
		log.Printf("Invalid value %+v for %s: %s\n", val, key, err)
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
	url  string
	mu   sync.Mutex
	nats NatsClient

	maxStreamBitrate int
	maxScreenBitrate int
	mcuTimeout       time.Duration

	gw      *JanusGateway
	session *JanusSession
	handle  *JanusHandle

	closeChan chan bool

	muClients sync.Mutex
	clients   map[clientInterface]bool

	publisherRoomIds map[string]uint64

	reconnectTimer    *time.Timer
	reconnectInterval time.Duration

	connectedSince time.Time
	onConnected    atomic.Value
	onDisconnected atomic.Value
}

func emptyOnConnected()    {}
func emptyOnDisconnected() {}

func NewMcuJanus(url string, config *goconf.ConfigFile, nats NatsClient) (Mcu, error) {
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
		nats:             nats,
		maxStreamBitrate: maxStreamBitrate,
		maxScreenBitrate: maxScreenBitrate,
		mcuTimeout:       mcuTimeout,
		closeChan:        make(chan bool, 1),
		clients:          make(map[clientInterface]bool),
		publisherRoomIds: make(map[string]uint64),

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
		m.handle.Detach(context.TODO())
		m.handle = nil
	}
	if m.session != nil {
		m.closeChan <- true
		m.session.Destroy(context.TODO())
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
	m.publisherRoomIds = make(map[string]uint64)
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
		log.Printf("Connection to Janus gateway was interrupted, reconnecting in %s\n", m.reconnectInterval)
	} else {
		log.Printf("Reconnect to Janus gateway failed (%s), reconnecting in %s\n", err, m.reconnectInterval)
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

	log.Printf("Connected to %s %s by %s\n", info.Name, info.VersionString, info.Author)
	if plugin, found := info.Plugins[pluginVideoRoom]; !found {
		return fmt.Errorf("Plugin %s is not supported", pluginVideoRoom)
	} else {
		log.Printf("Found %s %s by %s\n", plugin.Name, plugin.VersionString, plugin.Author)
	}

	if !info.DataChannels {
		return fmt.Errorf("Data channels are not supported")
	} else {
		log.Println("Data channels are supported")
	}

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
	result.Publishers = int64(len(m.publisherRoomIds))
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
	mu       sync.Mutex

	session    uint64
	roomId     uint64
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
}

func (c *mcuJanusClient) Id() string {
	return strconv.FormatUint(c.handleId, 10)
}

func (c *mcuJanusClient) StreamType() string {
	return c.streamType
}

func (c *mcuJanusClient) Close(ctx context.Context) {
}

func (c *mcuJanusClient) SendMessage(ctx context.Context, message *MessageClientMessage, data *MessageClientMessageData, callback func(error, map[string]interface{})) {
}

func (c *mcuJanusClient) closeClient(ctx context.Context) {
	if handle := c.handle; handle != nil {
		c.handle = nil
		c.closeChan <- true
		if _, err := handle.Detach(ctx); err != nil {
			if e, ok := err.(*janus.ErrorMsg); !ok || e.Err.Code != JANUS_ERROR_HANDLE_NOT_FOUND {
				log.Println("Could not detach client", handle.Id, err)
			}
		}
	}
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
				// Ignore
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

type mcuJanusPublisher struct {
	mcuJanusClient

	id string
}

func (m *mcuJanus) getOrCreatePublisherHandle(ctx context.Context, id string, streamType string) (*JanusHandle, uint64, uint64, error) {
	session := m.session
	if session == nil {
		return nil, 0, 0, ErrNotConnected
	}
	handle, err := session.Attach(ctx, pluginVideoRoom)
	if err != nil {
		return nil, 0, 0, err
	}

	log.Printf("Attached %s as publisher %d to plugin %s in session %d", streamType, handle.Id, pluginVideoRoom, session.Id)
	roomId, err := m.searchPublisherRoom(ctx, id, streamType)
	if err != nil {
		log.Printf("Could not search for room of publisher %s: %s", id, err)
	}

	if roomId == 0 {
		create_msg := map[string]interface{}{
			"request":     "create",
			"description": id + "|" + streamType,
			// We publish every stream in its own Janus room.
			"publishers": 1,
			// Do not use the video-orientation RTP extension as it breaks video
			// orientation changes in Firefox.
			"videoorient_ext": false,
		}
		if streamType == streamTypeScreen {
			create_msg["bitrate"] = m.maxScreenBitrate
		} else {
			create_msg["bitrate"] = m.maxStreamBitrate
		}
		create_response, err := handle.Request(ctx, create_msg)
		if err != nil {
			handle.Detach(ctx)
			return nil, 0, 0, err
		}

		roomId = getPluginIntValue(create_response.PluginData, pluginVideoRoom, "room")
		if roomId == 0 {
			handle.Detach(ctx)
			return nil, 0, 0, fmt.Errorf("No room id received: %+v", create_response)
		}

		log.Println("Created room", roomId, create_response.PluginData)
	} else {
		log.Println("Use existing room", roomId)
	}

	msg := map[string]interface{}{
		"request": "join",
		"ptype":   "publisher",
		"room":    roomId,
		"id":      streamTypeUserIds[streamType],
	}

	response, err := handle.Message(ctx, msg, nil)
	if err != nil {
		handle.Detach(ctx)
		return nil, 0, 0, err
	}

	return handle, response.Session, roomId, nil
}

func (m *mcuJanus) NewPublisher(ctx context.Context, listener McuListener, id string, streamType string, initiator McuInitiator) (McuPublisher, error) {
	if _, found := streamTypeUserIds[streamType]; !found {
		return nil, fmt.Errorf("Unsupported stream type %s", streamType)
	}

	handle, session, roomId, err := m.getOrCreatePublisherHandle(ctx, id, streamType)
	if err != nil {
		return nil, err
	}

	client := &mcuJanusPublisher{
		mcuJanusClient: mcuJanusClient{
			mcu:      m,
			listener: listener,

			session:    session,
			roomId:     roomId,
			streamType: streamType,

			handle:    handle,
			handleId:  handle.Id,
			closeChan: make(chan bool, 1),
			deferred:  make(chan func(), 64),
		},
		id: id,
	}
	client.mcuJanusClient.handleEvent = client.handleEvent
	client.mcuJanusClient.handleHangup = client.handleHangup
	client.mcuJanusClient.handleDetached = client.handleDetached
	client.mcuJanusClient.handleConnected = client.handleConnected
	client.mcuJanusClient.handleSlowLink = client.handleSlowLink
	m.mu.Lock()
	m.publisherRoomIds[id+"|"+streamType] = roomId
	m.mu.Unlock()

	m.registerClient(client)
	if err := client.publishNats("created"); err != nil {
		log.Printf("Could not publish \"created\" event for publisher %s: %s\n", id, err)
	}
	go client.run(handle, client.closeChan)
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
	if err := p.publishNats("connected"); err != nil {
		log.Printf("Could not publish \"connected\" event for publisher %s: %s\n", p.id, err)
	}
}

func (p *mcuJanusPublisher) handleSlowLink(event *janus.SlowLinkMsg) {
	if event.Uplink {
		log.Printf("Publisher %s (%d) is reporting %d NACKs on the uplink (Janus -> client)", p.listener.PublicId(), p.handleId, event.Nacks)
	} else {
		log.Printf("Publisher %s (%d) is reporting %d NACKs on the downlink (client -> Janus)", p.listener.PublicId(), p.handleId, event.Nacks)
	}
}

func (p *mcuJanusPublisher) publishNats(messageType string) error {
	return p.mcu.nats.PublishNats("publisher-"+p.id+"|"+p.streamType, &NatsMessage{Type: messageType})
}

func (p *mcuJanusPublisher) NotifyReconnected() {
	ctx := context.TODO()
	handle, session, roomId, err := p.mcu.getOrCreatePublisherHandle(ctx, p.id, p.streamType)
	if err != nil {
		log.Printf("Could not reconnect publisher %s: %s\n", p.id, err)
		// TODO(jojo): Retry
		return
	}

	p.handle = handle
	p.handleId = handle.Id
	p.session = session
	p.roomId = roomId

	p.mcu.mu.Lock()
	p.mcu.publisherRoomIds[p.id+"|"+p.streamType] = roomId
	p.mcu.mu.Unlock()
	log.Printf("Publisher %s reconnected\n", p.id)
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
		delete(p.mcu.publisherRoomIds, p.id+"|"+p.streamType)
		p.mcu.mu.Unlock()
		p.roomId = 0
		notify = true
	}
	p.closeClient(ctx)
	p.mu.Unlock()

	if notify {
		p.mcu.unregisterClient(p)
		p.listener.PublisherClosed(p)
	}
}

func (p *mcuJanusPublisher) SendMessage(ctx context.Context, message *MessageClientMessage, data *MessageClientMessageData, callback func(error, map[string]interface{})) {
	jsep_msg := data.Payload
	switch data.Type {
	case "offer":
		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			p.sendOffer(msgctx, jsep_msg, callback)
		}
	case "candidate":
		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			p.sendCandidate(msgctx, jsep_msg["candidate"], callback)
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

func (m *mcuJanus) lookupPublisherRoom(ctx context.Context, publisher string, streamType string) (uint64, error) {
	handle := m.handle
	if handle == nil {
		return 0, ErrNotConnected
	}
	list_msg := map[string]interface{}{
		"request": "list",
	}
	response_msg, err := handle.Request(ctx, list_msg)
	if err != nil {
		return 0, err
	}
	list, found := response_msg.PluginData.Data["list"]
	if !found {
		return 0, fmt.Errorf("no room list received")
	}

	entries, ok := list.([]interface{})
	if !ok {
		return 0, fmt.Errorf("Unsupported list received: %+v (%s)", list, reflect.TypeOf(list))
	}

	for _, entry := range entries {
		if entry, ok := entry.(map[string]interface{}); ok {
			description, found := entry["description"]
			if !found {
				continue
			}
			if description, ok := description.(string); ok {
				if description != publisher+"|"+streamType {
					continue
				}

				roomIdInterface, found := entry["room"]
				if !found {
					continue
				}

				roomId, err := convertIntValue(roomIdInterface)
				if err != nil {
					return 0, fmt.Errorf("Invalid room id received: %+v: %s", entry, err)
				}

				return roomId, nil
			}
		}
	}

	return 0, nil
}

func (m *mcuJanus) searchPublisherRoom(ctx context.Context, publisher string, streamType string) (uint64, error) {
	// Check for publishers connected to this signaling server.
	m.mu.Lock()
	roomId, found := m.publisherRoomIds[publisher+"|"+streamType]
	m.mu.Unlock()
	if found {
		return roomId, nil
	}

	// Check for publishers connected to a different signaling server.
	roomId, err := m.lookupPublisherRoom(ctx, publisher, streamType)
	if err != nil {
		return 0, err
	}

	return roomId, nil
}

func (m *mcuJanus) getPublisherRoomId(ctx context.Context, publisher string, streamType string) (uint64, error) {
	// Do the direct check immediately as this should be the normal case.
	m.mu.Lock()
	roomId, found := m.publisherRoomIds[publisher+"|"+streamType]
	if found {
		m.mu.Unlock()
		return roomId, nil
	}

	wakeupChan := make(chan *nats.Msg, 1)
	sub, err := m.nats.Subscribe("publisher-"+publisher+"|"+streamType, wakeupChan)
	m.mu.Unlock()
	if err != nil {
		return 0, err
	}
	defer sub.Unsubscribe()

	for roomId == 0 {
		var err error
		if roomId, err = m.searchPublisherRoom(ctx, publisher, streamType); err != nil {
			log.Printf("Could not search for room of publisher %s: %s", publisher, err)
		} else if roomId > 0 {
			break
		}

		select {
		case <-wakeupChan:
			// We got the wakeup event through NATS, the publisher should be
			// ready now.
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return roomId, nil
}

func (m *mcuJanus) getOrCreateSubscriberHandle(ctx context.Context, publisher string, streamType string) (*JanusHandle, uint64, error) {
	var roomId uint64
	var err error
	if roomId, err = m.getPublisherRoomId(ctx, publisher, streamType); err != nil {
		return nil, 0, err
	}

	session := m.session
	if session == nil {
		return nil, 0, ErrNotConnected
	}

	handle, err := session.Attach(ctx, pluginVideoRoom)
	if err != nil {
		return nil, 0, err
	}

	log.Printf("Attached subscriber to room %d of publisher %s in plugin %s in session %d as %d", roomId, publisher, pluginVideoRoom, session.Id, handle.Id)
	return handle, roomId, nil
}

func (m *mcuJanus) NewSubscriber(ctx context.Context, listener McuListener, publisher string, streamType string) (McuSubscriber, error) {
	if _, found := streamTypeUserIds[streamType]; !found {
		return nil, fmt.Errorf("Unsupported stream type %s", streamType)
	}

	handle, roomId, err := m.getOrCreateSubscriberHandle(ctx, publisher, streamType)
	if err != nil {
		return nil, err
	}

	client := &mcuJanusSubscriber{
		mcuJanusClient: mcuJanusClient{
			mcu:      m,
			listener: listener,

			roomId:     roomId,
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
	m.registerClient(client)
	go client.run(handle, client.closeChan)
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
			// Ignore events like selected substream / temporal layer.
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
}

func (p *mcuJanusSubscriber) handleSlowLink(event *janus.SlowLinkMsg) {
	if event.Uplink {
		log.Printf("Subscriber %s (%d) is reporting %d NACKs on the uplink (Janus -> client)", p.listener.PublicId(), p.handleId, event.Nacks)
	} else {
		log.Printf("Subscriber %s (%d) is reporting %d NACKs on the downlink (client -> Janus)", p.listener.PublicId(), p.handleId, event.Nacks)
	}
}

func (p *mcuJanusSubscriber) NotifyReconnected() {
	ctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
	defer cancel()
	handle, roomId, err := p.mcu.getOrCreateSubscriberHandle(ctx, p.publisher, p.streamType)
	if err != nil {
		// TODO(jojo): Retry?
		log.Printf("Could not reconnect subscriber for publisher %s: %s\n", p.publisher, err)
		p.Close(context.Background())
		return
	}

	p.handle = handle
	p.handleId = handle.Id
	p.roomId = roomId
	log.Printf("Reconnected subscriber for publisher %s\n", p.publisher)
}

func (p *mcuJanusSubscriber) Close(ctx context.Context) {
	p.mu.Lock()
	p.closeClient(ctx)
	p.mu.Unlock()

	p.mcu.unregisterClient(p)
	p.listener.SubscriberClosed(p)
}

func (p *mcuJanusSubscriber) joinRoom(ctx context.Context, callback func(error, map[string]interface{})) {
	handle := p.handle
	if handle == nil {
		callback(ErrNotConnected, nil)
		return
	}

	wakeupChan := make(chan *nats.Msg, 1)
	sub, err := p.mcu.nats.Subscribe("publisher-"+p.publisher+"|"+p.streamType, wakeupChan)
	if err != nil {
		callback(err, nil)
		return
	}
	defer sub.Unsubscribe()

retry:
	join_msg := map[string]interface{}{
		"request": "join",
		"ptype":   "listener",
		"room":    p.roomId,
		"feed":    streamTypeUserIds[p.streamType],
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

			var roomId uint64
			handle, roomId, err = p.mcu.getOrCreateSubscriberHandle(ctx, p.publisher, p.streamType)
			if err != nil {
				callback(fmt.Errorf("Already connected as subscriber for %s, error during re-joining: %s", p.streamType, err), nil)
				return
			}

			p.handle = handle
			p.handleId = handle.Id
			p.roomId = roomId
			p.closeChan = make(chan bool, 1)
			go p.run(p.handle, p.closeChan)
			log.Printf("Already connected as subscriber for %s, leaving and re-joining", p.streamType)
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
		wait:
			select {
			case msg := <-wakeupChan:
				var message NatsMessage
				if err := p.mcu.nats.Decode(msg, &message); err != nil {
					log.Printf("Error decoding wakeup NATS message %s (%s)\n", string(msg.Data), err)
					goto wait
				} else if message.Type != "connected" {
					log.Printf("Unsupported NATS message waiting for publisher %s: %+v\n", p.publisher, message)
					goto wait
				}
				log.Printf("Retry subscribing %s from %s", p.streamType, p.publisher)
			case <-ctx.Done():
				callback(ctx.Err(), nil)
				return
			}
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

func (p *mcuJanusSubscriber) SendMessage(ctx context.Context, message *MessageClientMessage, data *MessageClientMessageData, callback func(error, map[string]interface{})) {
	jsep_msg := data.Payload
	switch data.Type {
	case "requestoffer":
		fallthrough
	case "sendoffer":
		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			p.joinRoom(msgctx, callback)
		}
	case "answer":
		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			p.sendAnswer(msgctx, jsep_msg, callback)
		}
	case "candidate":
		p.deferred <- func() {
			msgctx, cancel := context.WithTimeout(context.Background(), p.mcu.mcuTimeout)
			defer cancel()

			p.sendCandidate(msgctx, jsep_msg["candidate"], callback)
		}
	case "endOfCandidates":
		// Ignore
	default:
		// Return error asynchronously
		go callback(fmt.Errorf("Unsupported message type: %s", data.Type), nil)
	}
}
