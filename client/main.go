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
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	pseudorand "math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/mailru/easyjson"

	signaling "github.com/strukturag/nextcloud-spreed-signaling"
)

var (
	addr = flag.String("addr", "localhost:28080", "http service address")

	config = flag.String("config", "server.conf", "config file to use")

	maxClients = flag.Int("maxClients", 100, "number of client connections")

	backendSecret []byte

	// Report messages that took more than 1 second.
	messageReportDuration = 1000 * time.Millisecond
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 64 * 1024
)

type Stats struct {
	numRecvMessages   atomic.Uint64
	numSentMessages   atomic.Uint64
	resetRecvMessages uint64
	resetSentMessages uint64

	start time.Time
}

func (s *Stats) reset(start time.Time) {
	s.resetRecvMessages = s.numRecvMessages.Load()
	s.resetSentMessages = s.numSentMessages.Load()
	s.start = start
}

func (s *Stats) Log() {
	now := time.Now()
	duration := now.Sub(s.start)
	perSec := uint64(duration / time.Second)
	if perSec == 0 {
		return
	}

	totalSentMessages := s.numSentMessages.Load()
	sentMessages := totalSentMessages - s.resetSentMessages
	totalRecvMessages := s.numRecvMessages.Load()
	recvMessages := totalRecvMessages - s.resetRecvMessages
	log.Printf("Stats: sent=%d (%d/sec), recv=%d (%d/sec), delta=%d",
		totalSentMessages, sentMessages/perSec,
		totalRecvMessages, recvMessages/perSec,
		totalSentMessages-totalRecvMessages)
	s.reset(now)
}

type MessagePayload struct {
	Now time.Time `json:"now"`
}

type SignalingClient struct {
	readyWg *sync.WaitGroup
	cookie  *signaling.SessionIdCodec

	conn *websocket.Conn

	stats  *Stats
	closed atomic.Bool

	stopChan chan struct{}

	lock             sync.Mutex
	privateSessionId string
	publicSessionId  string
	userId           string
}

func NewSignalingClient(cookie *signaling.SessionIdCodec, url string, stats *Stats, readyWg *sync.WaitGroup, doneWg *sync.WaitGroup) (*SignalingClient, error) {
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}

	client := &SignalingClient{
		readyWg: readyWg,
		cookie:  cookie,

		conn: conn,

		stats: stats,

		stopChan: make(chan struct{}),
	}
	doneWg.Add(2)
	go func() {
		defer doneWg.Done()
		client.readPump()
	}()
	go func() {
		defer doneWg.Done()
		client.writePump()
	}()
	return client, nil
}

func (c *SignalingClient) Close() {
	if !c.closed.CompareAndSwap(false, true) {
		return
	}

	// Signal writepump to terminate
	close(c.stopChan)

	c.lock.Lock()
	c.publicSessionId = ""
	c.privateSessionId = ""
	c.conn.SetWriteDeadline(time.Now().Add(writeWait))                                                          // nolint
	c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")) // nolint
	c.conn.Close()
	c.conn = nil
	c.lock.Unlock()
}

func (c *SignalingClient) Send(message *signaling.ClientMessage) {
	c.lock.Lock()
	if c.conn == nil {
		c.lock.Unlock()
		return
	}

	if !c.writeInternal(message) {
		c.lock.Unlock()
		c.Close()
		return
	}
	c.lock.Unlock()
}

func (c *SignalingClient) processMessage(message *signaling.ServerMessage) {
	c.stats.numRecvMessages.Add(1)
	switch message.Type {
	case "hello":
		c.processHelloMessage(message)
	case "message":
		c.processMessageMessage(message)
	case "bye":
		log.Printf("Received bye: %+v", message.Bye)
		c.Close()
	case "error":
		log.Printf("Received error: %+v", message.Error)
		c.Close()
	default:
		log.Printf("Unsupported message type: %+v", *message)
	}
}

func (c *SignalingClient) privateToPublicSessionId(privateId string) string {
	data, err := c.cookie.DecodePrivate(privateId)
	if err != nil {
		panic(fmt.Sprintf("could not decode private session id: %s", err))
	}
	publicId, err := c.cookie.EncodePublic(data)
	if err != nil {
		panic(fmt.Sprintf("could not encode public id: %s", err))
	}
	return publicId
}

func (c *SignalingClient) processHelloMessage(message *signaling.ServerMessage) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.privateSessionId = message.Hello.ResumeId
	c.publicSessionId = c.privateToPublicSessionId(c.privateSessionId)
	c.userId = message.Hello.UserId
	log.Printf("Registered as %s (userid %s)", c.privateSessionId, c.userId)
	c.readyWg.Done()
}

func (c *SignalingClient) PublicSessionId() string {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.publicSessionId
}

func (c *SignalingClient) processMessageMessage(message *signaling.ServerMessage) {
	var msg MessagePayload
	if err := json.Unmarshal(message.Message.Data, &msg); err != nil {
		log.Println("Error in unmarshal", err)
		return
	}

	now := time.Now()
	duration := now.Sub(msg.Now)
	if duration > messageReportDuration {
		log.Printf("Message took %s", duration)
	}
}

func (c *SignalingClient) readPump() {
	conn := c.conn

	defer func() {
		conn.Close()
	}()

	conn.SetReadLimit(maxMessageSize)
	conn.SetReadDeadline(time.Now().Add(pongWait)) // nolint
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait)) // nolint
		return nil
	})

	var decodeBuffer bytes.Buffer
	for {
		conn.SetReadDeadline(time.Now().Add(pongWait)) // nolint
		messageType, reader, err := conn.NextReader()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure,
				websocket.CloseGoingAway,
				websocket.CloseNoStatusReceived) {
				log.Printf("Error: %v", err)
			}
			break
		}

		if messageType != websocket.TextMessage {
			log.Println("Unsupported message type", messageType)
			break
		}

		decodeBuffer.Reset()
		if _, err := decodeBuffer.ReadFrom(reader); err != nil {
			c.lock.Lock()
			if c.conn != nil {
				log.Println("Error reading message", err)
			}
			c.lock.Unlock()
			break
		}

		var message signaling.ServerMessage
		if err := message.UnmarshalJSON(decodeBuffer.Bytes()); err != nil {
			log.Printf("Error: %v", err)
			break
		}

		c.processMessage(&message)
	}
}

func (c *SignalingClient) writeInternal(message *signaling.ClientMessage) bool {
	var closeData []byte

	c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	writer, err := c.conn.NextWriter(websocket.TextMessage)
	if err == nil {
		_, err = easyjson.MarshalToWriter(message, writer)
	}
	if err != nil {
		if err == websocket.ErrCloseSent {
			// Already sent a "close", won't be able to send anything else.
			return false
		}

		log.Println("Could not send message", message, err)
		// TODO(jojo): Differentiate between JSON encode errors and websocket errors.
		closeData = websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "")
		goto close
	}

	writer.Close()
	c.stats.numSentMessages.Add(1)
	return true

close:
	c.conn.SetWriteDeadline(time.Now().Add(writeWait))     // nolint
	c.conn.WriteMessage(websocket.CloseMessage, closeData) // nolint
	return false
}

func (c *SignalingClient) sendPing() bool {
	c.lock.Lock()
	defer c.lock.Unlock()
	if c.conn == nil {
		return false
	}

	c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	if err := c.conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
		return false
	}

	return true
}

func (c *SignalingClient) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case <-ticker.C:
			if !c.sendPing() {
				return
			}
		case <-c.stopChan:
			return
		}
	}
}

func (c *SignalingClient) SendMessages(clients []*SignalingClient) {
	sessionIds := make(map[*SignalingClient]string)
	for _, c := range clients {
		sessionIds[c] = c.PublicSessionId()
	}

	for !c.closed.Load() {
		now := time.Now()

		sender := c
		recipientIdx := pseudorand.Int() % len(clients)
		// Make sure a client is not sending to himself
		for clients[recipientIdx] == sender {
			recipientIdx = pseudorand.Int() % len(clients)
		}
		recipient := clients[recipientIdx]
		msgdata := MessagePayload{
			Now: now,
		}
		data, _ := json.Marshal(msgdata)
		msg := &signaling.ClientMessage{
			Type: "message",
			Message: &signaling.MessageClientMessage{
				Recipient: signaling.MessageClientMessageRecipient{
					Type:      "session",
					SessionId: sessionIds[recipient],
				},
				Data: data,
			},
		}
		sender.Send(msg)
		// Give some time to other clients.
		time.Sleep(1 * time.Millisecond)
	}
}

func registerAuthHandler(router *mux.Router) {
	router.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Println("Error reading body:", err)
			return
		}

		rnd := r.Header.Get(signaling.HeaderBackendSignalingRandom)
		checksum := r.Header.Get(signaling.HeaderBackendSignalingChecksum)
		if rnd == "" || checksum == "" {
			log.Println("No checksum headers found")
			return
		}

		if verify := signaling.CalculateBackendChecksum(rnd, body, backendSecret); verify != checksum {
			log.Println("Backend checksum verification failed")
			return
		}

		var request signaling.BackendClientRequest
		if err := request.UnmarshalJSON(body); err != nil {
			log.Println(err)
			return
		}

		response := &signaling.BackendClientResponse{
			Type: "auth",
			Auth: &signaling.BackendClientAuthResponse{
				Version: signaling.BackendVersion,
				UserId:  "sample-user",
			},
		}

		data, err := response.MarshalJSON()
		if err != nil {
			log.Println(err)
			return
		}

		rawdata := json.RawMessage(data)
		payload := &signaling.OcsResponse{
			Ocs: &signaling.OcsBody{
				Meta: signaling.OcsMeta{
					Status:     "ok",
					StatusCode: http.StatusOK,
					Message:    http.StatusText(http.StatusOK),
				},
				Data: rawdata,
			},
		}

		jsonpayload, err := payload.MarshalJSON()
		if err != nil {
			log.Println(err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(jsonpayload) // nolint
	})
}

func getLocalIP() string {
	interfaces, err := net.InterfaceAddrs()
	if err != nil {
		log.Fatal(err)
	}
	for _, intf := range interfaces {
		switch t := intf.(type) {
		case *net.IPNet:
			if !t.IP.IsInterfaceLocalMulticast() && !t.IP.IsLoopback() {
				return t.IP.String()
			}
		}
	}
	return ""
}

func main() {
	flag.Parse()
	log.SetFlags(0)

	config, err := goconf.ReadConfigFile(*config)
	if err != nil {
		log.Fatal("Could not read configuration: ", err)
	}

	secret, _ := signaling.GetStringOptionWithEnv(config, "backend", "secret")
	backendSecret = []byte(secret)

	hashKey, _ := signaling.GetStringOptionWithEnv(config, "sessions", "hashkey")
	switch len(hashKey) {
	case 32:
	case 64:
	default:
		log.Printf("WARNING: The sessions hash key should be 32 or 64 bytes but is %d bytes", len(hashKey))
	}

	blockKey, _ := signaling.GetStringOptionWithEnv(config, "sessions", "blockkey")
	blockBytes := []byte(blockKey)
	switch len(blockKey) {
	case 0:
		blockBytes = nil
	case 16:
	case 24:
	case 32:
	default:
		log.Fatalf("The sessions block key must be 16, 24 or 32 bytes but is %d bytes", len(blockKey))
	}
	cookie := signaling.NewSessionIdCodec([]byte(hashKey), blockBytes)

	log.Printf("Using a maximum of %d CPUs", runtime.GOMAXPROCS(0))

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	r := mux.NewRouter()
	registerAuthHandler(r)

	localIP := getLocalIP()
	listener, err := net.Listen("tcp", localIP+":0")
	if err != nil {
		log.Fatal(err)
	}

	server := http.Server{
		Handler: r,
	}
	go func() {
		server.Serve(listener) // nolint
	}()
	backendUrl := "http://" + listener.Addr().String()
	log.Println("Backend server running on", backendUrl)

	urls := make([]url.URL, 0)
	urlstrings := make([]string, 0)
	for host := range signaling.SplitEntries(*addr, ",") {
		u := url.URL{
			Scheme: "ws",
			Host:   host,
			Path:   "/spreed",
		}
		urls = append(urls, u)
		urlstrings = append(urlstrings, u.String())
	}
	log.Printf("Connecting to %s", urlstrings)

	clients := make([]*SignalingClient, 0)
	stats := &Stats{}

	if *maxClients < 2 {
		log.Fatalf("Need at least 2 clients, got %d", *maxClients)
	}

	log.Printf("Starting %d clients", *maxClients)

	var doneWg sync.WaitGroup
	var readyWg sync.WaitGroup

	for i := 0; i < *maxClients; i++ {
		client, err := NewSignalingClient(cookie, urls[i%len(urls)].String(), stats, &readyWg, &doneWg)
		if err != nil {
			log.Fatal(err)
		}
		defer client.Close()
		readyWg.Add(1)

		request := &signaling.ClientMessage{
			Type: "hello",
			Hello: &signaling.HelloClientMessage{
				Version: signaling.HelloVersionV1,
				Auth: &signaling.HelloClientMessageAuth{
					Url:    backendUrl + "/auth",
					Params: json.RawMessage("{}"),
				},
			},
		}

		client.Send(request)
		clients = append(clients, client)
	}

	log.Println("Clients created")
	readyWg.Wait()

	log.Println("All connections established")

	for _, c := range clients {
		doneWg.Add(1)
		go func(c *SignalingClient) {
			defer doneWg.Done()
			c.SendMessages(clients)
		}(c)
	}

	stats.start = time.Now()
	reportInterval := 10 * time.Second
	report := time.NewTicker(reportInterval)
loop:
	for {
		select {
		case <-interrupt:
			log.Println("Interrupted")
			break loop
		case <-report.C:
			stats.Log()
		}
	}

	log.Println("Waiting for clients to terminate ...")
	for _, c := range clients {
		c.Close()
	}
	doneWg.Wait()
}
