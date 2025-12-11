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
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/mailru/easyjson"
	"github.com/mailru/easyjson/jlexer"
	"github.com/mailru/easyjson/jwriter"

	signaling "github.com/strukturag/nextcloud-spreed-signaling"
	"github.com/strukturag/nextcloud-spreed-signaling/api"
	"github.com/strukturag/nextcloud-spreed-signaling/config"
	"github.com/strukturag/nextcloud-spreed-signaling/internal"
	"github.com/strukturag/nextcloud-spreed-signaling/talk"
)

var (
	version = "unreleased"

	showVersion = flag.Bool("version", false, "show version and quit")

	addr = flag.String("addr", "localhost:28080", "http service address")

	configFlag = flag.String("config", "server.conf", "config file to use")

	cpuprofile = flag.String("cpuprofile", "", "write cpu profile to file")

	memprofile = flag.String("memprofile", "", "write memory profile to file")

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

type MessagePayload struct {
	Now time.Time `json:"now"`
}

func (m *MessagePayload) MarshalJSON() ([]byte, error) {
	w := jwriter.Writer{}
	w.RawByte('{')
	w.RawString("\"now\":")
	w.Raw(m.Now.MarshalJSON())
	w.RawByte('}')
	return w.Buffer.BuildBytes(), w.Error
}

func (m *MessagePayload) UnmarshalJSON(data []byte) error {
	r := jlexer.Lexer{Data: data}
	r.Delim('{')
	for !r.IsDelim('}') {
		key := r.UnsafeFieldName(false)
		r.WantColon()
		switch key {
		case "now":
			if r.IsNull() {
				r.Skip()
			} else {
				if data := r.Raw(); r.Ok() {
					r.AddError((m.Now).UnmarshalJSON(data))
				}
			}
		default:
			r.SkipRecursive()
		}
		r.WantComma()
	}
	r.Delim('}')
	r.Consumed()

	return r.Error()
}

type SignalingClient struct {
	readyWg *sync.WaitGroup // +checklocksignore: Only written to from constructor.

	conn *websocket.Conn

	stats  *Stats
	closed atomic.Bool

	stopChan chan struct{}

	lock sync.Mutex
	// +checklocks:lock
	privateSessionId api.PrivateSessionId
	// +checklocks:lock
	publicSessionId api.PublicSessionId
	// +checklocks:lock
	userId string
}

func NewSignalingClient(url string, stats *Stats, readyWg *sync.WaitGroup, doneWg *sync.WaitGroup) (*SignalingClient, error) {
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}

	client := &SignalingClient{
		readyWg: readyWg,

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
	c.writeInternal(&api.ClientMessage{
		Type: "bye",
		Bye:  &api.ByeClientMessage{},
	})
	c.conn.SetWriteDeadline(time.Now().Add(writeWait))                                                          // nolint
	c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")) // nolint
	c.conn.Close()
	c.conn = nil
	c.lock.Unlock()
}

func (c *SignalingClient) Send(message *api.ClientMessage) {
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

func (c *SignalingClient) processMessage(message *api.ServerMessage) {
	c.stats.numRecvMessages.Add(1)
	switch message.Type {
	case "welcome":
		// Ignore welcome message.
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

func (c *SignalingClient) processHelloMessage(message *api.ServerMessage) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.privateSessionId = message.Hello.ResumeId
	c.publicSessionId = message.Hello.SessionId
	c.userId = message.Hello.UserId
	log.Printf("Registered as %s (userid %s)", c.privateSessionId, c.userId)
	c.readyWg.Done()
}

func (c *SignalingClient) PublicSessionId() api.PublicSessionId {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.publicSessionId
}

func (c *SignalingClient) processMessageMessage(message *api.ServerMessage) {
	var msg MessagePayload
	if err := msg.UnmarshalJSON(message.Message.Data); err != nil {
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

		c.stats.numRecvBytes.Add(uint64(decodeBuffer.Len()))

		var message api.ServerMessage
		if err := message.UnmarshalJSON(decodeBuffer.Bytes()); err != nil {
			log.Printf("Error: %v", err)
			break
		}

		c.processMessage(&message)
	}
}

func (c *SignalingClient) writeInternal(message *api.ClientMessage) bool {
	var closeData []byte

	c.conn.SetWriteDeadline(time.Now().Add(writeWait)) // nolint
	var written int
	writer, err := c.conn.NextWriter(websocket.TextMessage)
	if err == nil {
		written, err = easyjson.MarshalToWriter(message, writer)
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
	if written > 0 {
		c.stats.numSentBytes.Add(uint64(written))
	}
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
	sessionIds := make(map[*SignalingClient]api.PublicSessionId)
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
		data, _ := msgdata.MarshalJSON()
		msg := &api.ClientMessage{
			Type: "message",
			Message: &api.MessageClientMessage{
				Recipient: api.MessageClientMessageRecipient{
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
	router.HandleFunc("/ocs/v2.php/apps/spreed/api/v1/signaling/backend", func(w http.ResponseWriter, r *http.Request) {
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
		payload := &talk.OcsResponse{
			Ocs: &talk.OcsBody{
				Meta: talk.OcsMeta{
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

	if *showVersion {
		fmt.Printf("nextcloud-spreed-signaling-client version %s/%s\n", version, runtime.Version())
		os.Exit(0)
	}

	cfg, err := goconf.ReadConfigFile(*configFlag)
	if err != nil {
		log.Fatal("Could not read configuration: ", err)
	}

	secret, _ := config.GetStringOptionWithEnv(cfg, "backend", "secret")
	backendSecret = []byte(secret)

	log.Printf("Using a maximum of %d CPUs", runtime.GOMAXPROCS(0))

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}

		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatalf("Error writing CPU profile to %s: %s", *cpuprofile, err)
		}
		log.Printf("Writing CPU profile to %s ...", *cpuprofile)
		defer pprof.StopCPUProfile()
	}

	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			log.Fatal(err) // nolint (defer pprof.StopCPUProfile() will not run which is ok in case of errors)
		}

		defer func() {
			log.Printf("Writing Memory profile to %s ...", *memprofile)
			runtime.GC()
			if err := pprof.WriteHeapProfile(f); err != nil {
				log.Printf("Error writing Memory profile to %s: %s", *memprofile, err)
			}
		}()
	}

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
	for host := range internal.SplitEntries(*addr, ",") {
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
		client, err := NewSignalingClient(urls[i%len(urls)].String(), stats, &readyWg, &doneWg)
		if err != nil {
			log.Fatal(err)
		}
		defer client.Close()
		readyWg.Add(1)

		request := &api.ClientMessage{
			Type: "hello",
			Hello: &api.HelloClientMessage{
				Version: api.HelloVersionV1,
				Auth: &api.HelloClientMessageAuth{
					Url:    backendUrl,
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
