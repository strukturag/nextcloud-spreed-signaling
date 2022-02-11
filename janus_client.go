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

/**
 * Contents heavily based on
 * https://github.com/notedit/janus-go/blob/master/janus.go
 *
 * Added error handling and improve functionality.
 */
package signaling

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/notedit/janus-go"
)

const (
	/*! \brief Success (no error) */
	JANUS_OK = 0

	/*! \brief Unauthorized (can only happen when using apisecret/auth token) */
	JANUS_ERROR_UNAUTHORIZED = 403
	/*! \brief Unauthorized access to a plugin (can only happen when using auth token) */
	JANUS_ERROR_UNAUTHORIZED_PLUGIN = 405
	/*! \brief Unknown/undocumented error */
	JANUS_ERROR_UNKNOWN = 490
	/*! \brief Transport related error */
	JANUS_ERROR_TRANSPORT_SPECIFIC = 450
	/*! \brief The request is missing in the message */
	JANUS_ERROR_MISSING_REQUEST = 452
	/*! \brief The gateway does not suppurt this request */
	JANUS_ERROR_UNKNOWN_REQUEST = 453
	/*! \brief The payload is not a valid JSON message */
	JANUS_ERROR_INVALID_JSON = 454
	/*! \brief The object is not a valid JSON object as expected */
	JANUS_ERROR_INVALID_JSON_OBJECT = 455
	/*! \brief A mandatory element is missing in the message */
	JANUS_ERROR_MISSING_MANDATORY_ELEMENT = 456
	/*! \brief The request cannot be handled for this webserver path  */
	JANUS_ERROR_INVALID_REQUEST_PATH = 457
	/*! \brief The session the request refers to doesn't exist */
	JANUS_ERROR_SESSION_NOT_FOUND = 458
	/*! \brief The handle the request refers to doesn't exist */
	JANUS_ERROR_HANDLE_NOT_FOUND = 459
	/*! \brief The plugin the request wants to talk to doesn't exist */
	JANUS_ERROR_PLUGIN_NOT_FOUND = 460
	/*! \brief An error occurring when trying to attach to a plugin and create a handle  */
	JANUS_ERROR_PLUGIN_ATTACH = 461
	/*! \brief An error occurring when trying to send a message/request to the plugin */
	JANUS_ERROR_PLUGIN_MESSAGE = 462
	/*! \brief An error occurring when trying to detach from a plugin and destroy the related handle  */
	JANUS_ERROR_PLUGIN_DETACH = 463
	/*! \brief The gateway doesn't support this SDP type
	 * \todo The gateway currently only supports OFFER and ANSWER. */
	JANUS_ERROR_JSEP_UNKNOWN_TYPE = 464
	/*! \brief The Session Description provided by the peer is invalid */
	JANUS_ERROR_JSEP_INVALID_SDP = 465
	/*! \brief The stream a trickle candidate for does not exist or is invalid */
	JANUS_ERROR_TRICKE_INVALID_STREAM = 466
	/*! \brief A JSON element is of the wrong type (e.g., an integer instead of a string) */
	JANUS_ERROR_INVALID_ELEMENT_TYPE = 467
	/*! \brief The ID provided to create a new session is already in use */
	JANUS_ERROR_SESSION_CONFLICT = 468
	/*! \brief We got an ANSWER to an OFFER we never made */
	JANUS_ERROR_UNEXPECTED_ANSWER = 469
	/*! \brief The auth token the request refers to doesn't exist */
	JANUS_ERROR_TOKEN_NOT_FOUND = 470

	// Error codes of videoroom plugin.
	JANUS_VIDEOROOM_ERROR_UNKNOWN_ERROR     = 499
	JANUS_VIDEOROOM_ERROR_NO_MESSAGE        = 421
	JANUS_VIDEOROOM_ERROR_INVALID_JSON      = 422
	JANUS_VIDEOROOM_ERROR_INVALID_REQUEST   = 423
	JANUS_VIDEOROOM_ERROR_JOIN_FIRST        = 424
	JANUS_VIDEOROOM_ERROR_ALREADY_JOINED    = 425
	JANUS_VIDEOROOM_ERROR_NO_SUCH_ROOM      = 426
	JANUS_VIDEOROOM_ERROR_ROOM_EXISTS       = 427
	JANUS_VIDEOROOM_ERROR_NO_SUCH_FEED      = 428
	JANUS_VIDEOROOM_ERROR_MISSING_ELEMENT   = 429
	JANUS_VIDEOROOM_ERROR_INVALID_ELEMENT   = 430
	JANUS_VIDEOROOM_ERROR_INVALID_SDP_TYPE  = 431
	JANUS_VIDEOROOM_ERROR_PUBLISHERS_FULL   = 432
	JANUS_VIDEOROOM_ERROR_UNAUTHORIZED      = 433
	JANUS_VIDEOROOM_ERROR_ALREADY_PUBLISHED = 434
	JANUS_VIDEOROOM_ERROR_NOT_PUBLISHED     = 435
	JANUS_VIDEOROOM_ERROR_ID_EXISTS         = 436
	JANUS_VIDEOROOM_ERROR_INVALID_SDP       = 437
)

var (
	janusDialer = websocket.Dialer{
		Subprotocols: []string{"janus-protocol"},
		Proxy:        http.ProxyFromEnvironment,
	}
)

var msgtypes = map[string]func() interface{}{
	"error":       func() interface{} { return &janus.ErrorMsg{} },
	"success":     func() interface{} { return &janus.SuccessMsg{} },
	"detached":    func() interface{} { return &janus.DetachedMsg{} },
	"server_info": func() interface{} { return &InfoMsg{} },
	"ack":         func() interface{} { return &janus.AckMsg{} },
	"event":       func() interface{} { return &janus.EventMsg{} },
	"webrtcup":    func() interface{} { return &janus.WebRTCUpMsg{} },
	"media":       func() interface{} { return &janus.MediaMsg{} },
	"hangup":      func() interface{} { return &janus.HangupMsg{} },
	"slowlink":    func() interface{} { return &janus.SlowLinkMsg{} },
	"timeout":     func() interface{} { return &janus.TimeoutMsg{} },
	"trickle":     func() interface{} { return &TrickleMsg{} },
}

type InfoMsg struct {
	Name          string
	Version       int
	VersionString string `json:"version_string"`
	Author        string
	DataChannels  bool   `json:"data_channels"`
	IPv6          bool   `json:"ipv6"`
	LocalIP       string `json:"local-ip"`
	ICE_TCP       bool   `json:"ice-tcp"`
	FullTrickle   bool   `json:"full-trickle"`
	Transports    map[string]janus.PluginInfo
	Plugins       map[string]janus.PluginInfo
}

type TrickleMsg struct {
	Session   uint64 `json:"session_id"`
	Handle    uint64 `json:"sender"`
	Candidate struct {
		SdpMid        string `json:"sdpMid"`
		SdpMLineIndex int    `json:"sdpMLineIndex"`
		Candidate     string `json:"candidate"`

		Completed bool `json:"completed,omitempty"`
	} `json:"candidate"`
}

func unexpected(request string) error {
	return fmt.Errorf("unexpected response received to '%s' request", request)
}

type transaction struct {
	ch       chan interface{}
	incoming chan interface{}
	quitChan chan bool
}

func (t *transaction) run() {
	for {
		select {
		case msg := <-t.incoming:
			t.ch <- msg
		case <-t.quitChan:
			return
		}
	}
}

func (t *transaction) add(msg interface{}) {
	t.incoming <- msg
}

func (t *transaction) quit() {
	select {
	case t.quitChan <- true:
	default:
		// Already scheduled to quit.
	}
}

func newTransaction() *transaction {
	t := &transaction{
		ch:       make(chan interface{}, 1),
		incoming: make(chan interface{}, 8),
		quitChan: make(chan bool, 1),
	}
	return t
}

func newRequest(method string) (map[string]interface{}, *transaction) {
	req := make(map[string]interface{}, 8)
	req["janus"] = method
	return req, newTransaction()
}

type GatewayListener interface {
	ConnectionInterrupted()
}

type dummyGatewayListener struct {
}

func (l *dummyGatewayListener) ConnectionInterrupted() {
}

// Gateway represents a connection to an instance of the Janus Gateway.
type JanusGateway struct {
	nextTransaction uint64

	listener GatewayListener

	// Sessions is a map of the currently active sessions to the gateway.
	Sessions map[uint64]*JanusSession

	// Access to the Sessions map should be synchronized with the Gateway.Lock()
	// and Gateway.Unlock() methods provided by the embedded sync.Mutex.
	sync.Mutex

	conn         *websocket.Conn
	transactions map[uint64]*transaction

	closeChan chan bool

	writeMu sync.Mutex
}

// Connect creates a new Gateway instance, connected to the Janus Gateway.
// path should be a filesystem path to the Unix Socket that the Unix transport
// is bound to.
// On success, a new Gateway object will be returned and error will be nil.
// func Connect(path string, netType string) (*JanusGateway, error) {
// 	conn, err := net.Dial(netType, path)
// 	if err != nil {
// 		return nil, err
// 	}

// 	gateway := new(Gateway)
// 	//gateway.conn = conn
// 	gateway.transactions = make(map[uint64]chan interface{})
// 	gateway.Sessions = make(map[uint64]*JanusSession)

// 	go gateway.recv()
// 	return gateway, nil
// }

func NewJanusGateway(wsURL string, listener GatewayListener) (*JanusGateway, error) {
	conn, _, err := janusDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, err
	}

	gateway := new(JanusGateway)
	gateway.conn = conn
	gateway.transactions = make(map[uint64]*transaction)
	gateway.Sessions = make(map[uint64]*JanusSession)
	gateway.closeChan = make(chan bool)
	if listener == nil {
		listener = new(dummyGatewayListener)
	}
	gateway.listener = listener

	go gateway.ping()
	go gateway.recv()
	return gateway, nil
}

// Close closes the underlying connection to the Gateway.
func (gateway *JanusGateway) Close() error {
	gateway.closeChan <- true
	gateway.writeMu.Lock()
	if gateway.conn == nil {
		gateway.writeMu.Unlock()
		return nil
	}

	err := gateway.conn.Close()
	gateway.conn = nil
	gateway.writeMu.Unlock()
	gateway.cancelTransactions()
	return err
}

func (gateway *JanusGateway) cancelTransactions() {
	msg := &janus.ErrorMsg{
		Err: janus.ErrorData{
			Code:   500,
			Reason: "cancelled",
		},
	}
	gateway.Lock()
	for _, t := range gateway.transactions {
		go func(t *transaction) {
			t.add(msg)
			t.quit()
		}(t)
	}
	gateway.transactions = make(map[uint64]*transaction)
	gateway.Unlock()
}

func (gateway *JanusGateway) removeTransaction(id uint64) {
	gateway.Lock()
	t, found := gateway.transactions[id]
	if found {
		delete(gateway.transactions, id)
	}
	gateway.Unlock()
	if t != nil {
		t.quit()
	}
}

func (gateway *JanusGateway) send(msg map[string]interface{}, t *transaction) (uint64, error) {
	id := atomic.AddUint64(&gateway.nextTransaction, 1)
	msg["transaction"] = strconv.FormatUint(id, 10)
	data, err := json.Marshal(msg)
	if err != nil {
		return 0, err
	}

	go t.run()
	gateway.Lock()
	gateway.transactions[id] = t
	gateway.Unlock()

	gateway.writeMu.Lock()
	if gateway.conn == nil {
		gateway.writeMu.Unlock()
		gateway.removeTransaction(id)
		return 0, fmt.Errorf("not connected")
	}

	err = gateway.conn.WriteMessage(websocket.TextMessage, data)
	gateway.writeMu.Unlock()
	if err != nil {
		gateway.removeTransaction(id)
		return 0, err
	}
	return id, nil
}

func passMsg(ch chan interface{}, msg interface{}) {
	ch <- msg
}

func (gateway *JanusGateway) ping() {
	ticker := time.NewTicker(time.Second * 30)
	defer ticker.Stop()

loop:
	for {
		select {
		case <-ticker.C:
			gateway.writeMu.Lock()
			if gateway.conn == nil {
				gateway.writeMu.Unlock()
				continue
			}

			err := gateway.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(20*time.Second))
			gateway.writeMu.Unlock()
			if err != nil {
				log.Println("Error sending ping to MCU:", err)
			}
		case <-gateway.closeChan:
			break loop
		}
	}
}

func (gateway *JanusGateway) recv() {
	var decodeBuffer bytes.Buffer
	for {
		// Read message from Gateway

		// Decode to Msg struct
		var base janus.BaseMsg

		gateway.writeMu.Lock()
		conn := gateway.conn
		gateway.writeMu.Unlock()
		if conn == nil {
			return
		}

		_, reader, err := conn.NextReader()
		if err != nil {
			log.Printf("conn.NextReader: %s", err)
			gateway.writeMu.Lock()
			gateway.conn = nil
			gateway.writeMu.Unlock()
			gateway.cancelTransactions()
			go gateway.listener.ConnectionInterrupted()
			return
		}

		decodeBuffer.Reset()
		if _, err := decodeBuffer.ReadFrom(reader); err != nil {
			log.Printf("decodeBuffer.ReadFrom: %s", err)
			gateway.writeMu.Lock()
			gateway.conn = nil
			gateway.writeMu.Unlock()
			gateway.cancelTransactions()
			go gateway.listener.ConnectionInterrupted()
			break
		}

		data := bytes.NewReader(decodeBuffer.Bytes())
		decoder := json.NewDecoder(data)
		decoder.UseNumber()
		if err := decoder.Decode(&base); err != nil {
			log.Printf("json.Unmarshal of %s: %s", decodeBuffer.String(), err)
			continue
		}

		typeFunc, ok := msgtypes[base.Type]
		if !ok {
			log.Printf("Unknown message type received: %s", decodeBuffer.String())
			continue
		}

		msg := typeFunc()
		data = bytes.NewReader(decodeBuffer.Bytes())
		decoder = json.NewDecoder(data)
		decoder.UseNumber()
		if err := decoder.Decode(&msg); err != nil {
			log.Printf("json.Unmarshal of %s: %s", decodeBuffer.String(), err)
			continue // Decode error
		}

		// Pass message on from here
		if base.ID == "" {
			// Is this a Handle event?
			if base.Handle == 0 {
				// Nope. No idea what's going on...
				// Error()
				log.Printf("Received event without handle, ignoring: %s", decodeBuffer.String())
			} else {
				// Lookup Session
				gateway.Lock()
				session := gateway.Sessions[base.Session]
				gateway.Unlock()
				if session == nil {
					log.Printf("Unable to deliver message %s. Session %d gone?", decodeBuffer.String(), base.Session)
					continue
				}

				// Lookup Handle
				session.Lock()
				handle := session.Handles[base.Handle]
				session.Unlock()
				if handle == nil {
					log.Printf("Unable to deliver message %s. Handle %d gone?", decodeBuffer.String(), base.Handle)
					continue
				}

				// Pass msg
				go passMsg(handle.Events, msg)
			}
		} else {
			id, err := strconv.ParseUint(base.ID, 10, 64)
			if err != nil {
				log.Printf("Could not decode transaction id %s: %s", base.ID, err)
				continue
			}

			// Lookup Transaction
			gateway.Lock()
			transaction := gateway.transactions[id]
			gateway.Unlock()
			if transaction == nil {
				// Error()
				log.Printf("Received event for unknown transaction, ignoring: %s", decodeBuffer.String())
				continue
			}

			// Pass msg
			transaction.add(msg)
		}
	}
}

func waitForMessage(ctx context.Context, t *transaction) (interface{}, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg := <-t.ch:
		return msg, nil
	}
}

// Info sends an info request to the Gateway.
// On success, an InfoMsg will be returned and error will be nil.
func (gateway *JanusGateway) Info(ctx context.Context) (*InfoMsg, error) {
	req, ch := newRequest("info")
	id, err := gateway.send(req, ch)
	if err != nil {
		return nil, err
	}
	defer gateway.removeTransaction(id)

	msg, err := waitForMessage(ctx, ch)
	if err != nil {
		return nil, err
	}

	switch msg := msg.(type) {
	case *InfoMsg:
		return msg, nil
	case *janus.ErrorMsg:
		return nil, msg
	}

	return nil, unexpected("info")
}

// Create sends a create request to the Gateway.
// On success, a new Session will be returned and error will be nil.
func (gateway *JanusGateway) Create(ctx context.Context) (*JanusSession, error) {
	req, ch := newRequest("create")
	id, err := gateway.send(req, ch)
	if err != nil {
		return nil, err
	}
	defer gateway.removeTransaction(id)

	msg, err := waitForMessage(ctx, ch)
	if err != nil {
		return nil, err
	}
	var success *janus.SuccessMsg
	switch msg := msg.(type) {
	case *janus.SuccessMsg:
		success = msg
	case *janus.ErrorMsg:
		return nil, msg
	}

	// Create new session
	session := new(JanusSession)
	session.gateway = gateway
	session.Id = success.Data.ID
	session.Handles = make(map[uint64]*JanusHandle)

	// Store this session
	gateway.Lock()
	gateway.Sessions[session.Id] = session
	gateway.Unlock()

	return session, nil
}

// Session represents a session instance on the Janus Gateway.
type JanusSession struct {
	// Id is the session_id of this session
	Id uint64

	// Handles is a map of plugin handles within this session
	Handles map[uint64]*JanusHandle

	// Access to the Handles map should be synchronized with the Session.Lock()
	// and Session.Unlock() methods provided by the embedded sync.Mutex.
	sync.Mutex

	gateway *JanusGateway
}

func (session *JanusSession) send(msg map[string]interface{}, t *transaction) (uint64, error) {
	msg["session_id"] = session.Id
	return session.gateway.send(msg, t)
}

// Attach sends an attach request to the Gateway within this session.
// plugin should be the unique string of the plugin to attach to.
// On success, a new Handle will be returned and error will be nil.
func (session *JanusSession) Attach(ctx context.Context, plugin string) (*JanusHandle, error) {
	req, ch := newRequest("attach")
	req["plugin"] = plugin
	id, err := session.send(req, ch)
	if err != nil {
		return nil, err
	}
	defer session.gateway.removeTransaction(id)

	msg, err := waitForMessage(ctx, ch)
	if err != nil {
		return nil, err
	}
	var success *janus.SuccessMsg
	switch msg := msg.(type) {
	case *janus.SuccessMsg:
		success = msg
	case *janus.ErrorMsg:
		return nil, msg
	}

	handle := new(JanusHandle)
	handle.session = session
	handle.Id = success.Data.ID
	handle.Events = make(chan interface{}, 8)

	session.Lock()
	session.Handles[handle.Id] = handle
	session.Unlock()

	return handle, nil
}

// KeepAlive sends a keep-alive request to the Gateway.
// On success, an AckMsg will be returned and error will be nil.
func (session *JanusSession) KeepAlive(ctx context.Context) (*janus.AckMsg, error) {
	req, ch := newRequest("keepalive")
	id, err := session.send(req, ch)
	if err != nil {
		return nil, err
	}
	defer session.gateway.removeTransaction(id)

	msg, err := waitForMessage(ctx, ch)
	if err != nil {
		return nil, err
	}
	switch msg := msg.(type) {
	case *janus.AckMsg:
		return msg, nil
	case *janus.ErrorMsg:
		return nil, msg
	}

	return nil, unexpected("keepalive")
}

// Destroy sends a destroy request to the Gateway to tear down this session.
// On success, the Session will be removed from the Gateway.Sessions map, an
// AckMsg will be returned and error will be nil.
func (session *JanusSession) Destroy(ctx context.Context) (*janus.AckMsg, error) {
	req, ch := newRequest("destroy")
	id, err := session.send(req, ch)
	if err != nil {
		return nil, err
	}
	defer session.gateway.removeTransaction(id)

	msg, err := waitForMessage(ctx, ch)
	if err != nil {
		return nil, err
	}
	var ack *janus.AckMsg
	switch msg := msg.(type) {
	case *janus.AckMsg:
		ack = msg
	case *janus.ErrorMsg:
		return nil, msg
	}

	// Remove this session from the gateway
	session.gateway.Lock()
	delete(session.gateway.Sessions, session.Id)
	session.gateway.Unlock()

	return ack, nil
}

// Handle represents a handle to a plugin instance on the Gateway.
type JanusHandle struct {
	// Id is the handle_id of this plugin handle
	Id uint64

	// Type   // pub  or sub
	Type string

	//User   // Userid
	User string

	// Events is a receive only channel that can be used to receive events
	// related to this handle from the gateway.
	Events chan interface{}

	session *JanusSession
}

func (handle *JanusHandle) send(msg map[string]interface{}, t *transaction) (uint64, error) {
	msg["handle_id"] = handle.Id
	return handle.session.send(msg, t)
}

// send sync request
func (handle *JanusHandle) Request(ctx context.Context, body interface{}) (*janus.SuccessMsg, error) {
	req, ch := newRequest("message")
	if body != nil {
		req["body"] = body
	}
	id, err := handle.send(req, ch)
	if err != nil {
		return nil, err
	}
	defer handle.session.gateway.removeTransaction(id)

	msg, err := waitForMessage(ctx, ch)
	if err != nil {
		return nil, err
	}
	switch msg := msg.(type) {
	case *janus.SuccessMsg:
		return msg, nil
	case *janus.ErrorMsg:
		return nil, msg
	}

	return nil, unexpected("message")
}

// Message sends a message request to a plugin handle on the Gateway.
// body should be the plugin data to be passed to the plugin, and jsep should
// contain an optional SDP offer/answer to establish a WebRTC PeerConnection.
// On success, an EventMsg will be returned and error will be nil.
func (handle *JanusHandle) Message(ctx context.Context, body, jsep interface{}) (*janus.EventMsg, error) {
	req, ch := newRequest("message")
	if body != nil {
		req["body"] = body
	}
	if jsep != nil {
		req["jsep"] = jsep
	}
	id, err := handle.send(req, ch)
	if err != nil {
		return nil, err
	}
	defer handle.session.gateway.removeTransaction(id)

GetMessage: // No tears..
	msg, err := waitForMessage(ctx, ch)
	if err != nil {
		return nil, err
	}
	switch msg := msg.(type) {
	case *janus.AckMsg:
		goto GetMessage // ..only dreams.
	case *janus.EventMsg:
		return msg, nil
	case *janus.ErrorMsg:
		return nil, msg
	}

	return nil, unexpected("message")
}

// Trickle sends a trickle request to the Gateway as part of establishing
// a new PeerConnection with a plugin.
// candidate should be a single ICE candidate, or a completed object to
// signify that all candidates have been sent:
//		{
//			"completed": true
//		}
// On success, an AckMsg will be returned and error will be nil.
func (handle *JanusHandle) Trickle(ctx context.Context, candidate interface{}) (*janus.AckMsg, error) {
	req, ch := newRequest("trickle")
	req["candidate"] = candidate
	id, err := handle.send(req, ch)
	if err != nil {
		return nil, err
	}
	defer handle.session.gateway.removeTransaction(id)

	msg, err := waitForMessage(ctx, ch)
	if err != nil {
		return nil, err
	}
	switch msg := msg.(type) {
	case *janus.AckMsg:
		return msg, nil
	case *janus.ErrorMsg:
		return nil, msg
	}

	return nil, unexpected("trickle")
}

// TrickleMany sends a trickle request to the Gateway as part of establishing
// a new PeerConnection with a plugin.
// candidates should be an array of ICE candidates.
// On success, an AckMsg will be returned and error will be nil.
func (handle *JanusHandle) TrickleMany(ctx context.Context, candidates interface{}) (*janus.AckMsg, error) {
	req, ch := newRequest("trickle")
	req["candidates"] = candidates
	id, err := handle.send(req, ch)
	if err != nil {
		return nil, err
	}
	handle.session.gateway.removeTransaction(id)

	msg, err := waitForMessage(ctx, ch)
	if err != nil {
		return nil, err
	}
	switch msg := msg.(type) {
	case *janus.AckMsg:
		return msg, nil
	case *janus.ErrorMsg:
		return nil, msg
	}

	return nil, unexpected("trickle")
}

// Detach sends a detach request to the Gateway to remove this handle.
// On success, an AckMsg will be returned and error will be nil.
func (handle *JanusHandle) Detach(ctx context.Context) (*janus.AckMsg, error) {
	req, ch := newRequest("detach")
	id, err := handle.send(req, ch)
	if err != nil {
		return nil, err
	}
	defer handle.session.gateway.removeTransaction(id)

	msg, err := waitForMessage(ctx, ch)
	if err != nil {
		return nil, err
	}
	var ack *janus.AckMsg
	switch msg := msg.(type) {
	case *janus.AckMsg:
		ack = msg
	case *janus.ErrorMsg:
		return nil, msg
	}

	// Remove this handle from the session
	handle.session.Lock()
	delete(handle.session.Handles, handle.Id)
	handle.session.Unlock()

	return ack, nil
}
