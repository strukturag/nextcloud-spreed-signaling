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
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/dlintw/goconf"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	maxBodySize = 256 * 1024

	randomUsernameLength = 32

	sessionIdNotInMeeting = "0"
)

type BackendServer struct {
	hub          *Hub
	nats         NatsClient
	roomSessions RoomSessions

	version        string
	welcomeMessage string

	turnapikey  string
	turnsecret  []byte
	turnvalid   time.Duration
	turnservers []string

	statsAllowedIps map[string]bool
	invalidSecret   []byte
}

func NewBackendServer(config *goconf.ConfigFile, hub *Hub, version string) (*BackendServer, error) {
	turnapikey, _ := config.GetString("turn", "apikey")
	turnsecret, _ := config.GetString("turn", "secret")
	turnservers, _ := config.GetString("turn", "servers")
	// TODO(jojo): Make the validity for TURN credentials configurable.
	turnvalid := 24 * time.Hour

	var turnserverslist []string
	for _, s := range strings.Split(turnservers, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			turnserverslist = append(turnserverslist, s)
		}
	}

	if len(turnserverslist) != 0 {
		if turnapikey == "" {
			return nil, fmt.Errorf("need a TURN API key if TURN servers are configured")
		}
		if turnsecret == "" {
			return nil, fmt.Errorf("need a shared TURN secret if TURN servers are configured")
		}

		log.Printf("Using configured TURN API key")
		log.Printf("Using configured shared TURN secret")
		for _, s := range turnserverslist {
			log.Printf("Adding \"%s\" as TURN server", s)
		}
	}

	statsAllowed, _ := config.GetString("stats", "allowed_ips")
	var statsAllowedIps map[string]bool
	if statsAllowed == "" {
		log.Printf("No IPs configured for the stats endpoint, only allowing access from 127.0.0.1")
		statsAllowedIps = map[string]bool{
			"127.0.0.1": true,
		}
	} else {
		log.Printf("Only allowing access to the stats endpoing from %s", statsAllowed)
		statsAllowedIps = make(map[string]bool)
		for _, ip := range strings.Split(statsAllowed, ",") {
			ip = strings.TrimSpace(ip)
			if ip != "" {
				statsAllowedIps[ip] = true
			}
		}
	}

	invalidSecret := make([]byte, 32)
	if _, err := rand.Read(invalidSecret); err != nil {
		return nil, err
	}

	return &BackendServer{
		hub:          hub,
		nats:         hub.nats,
		roomSessions: hub.roomSessions,
		version:      version,

		turnapikey:  turnapikey,
		turnsecret:  []byte(turnsecret),
		turnvalid:   turnvalid,
		turnservers: turnserverslist,

		statsAllowedIps: statsAllowedIps,
		invalidSecret:   invalidSecret,
	}, nil
}

func (b *BackendServer) Start(r *mux.Router) error {
	welcome := map[string]string{
		"nextcloud-spreed-signaling": "Welcome",
		"version":                    b.version,
	}
	welcomeMessage, err := json.Marshal(welcome)
	if err != nil {
		// Should never happen.
		return err
	}

	b.welcomeMessage = string(welcomeMessage) + "\n"

	s := r.PathPrefix("/api/v1").Subrouter()
	s.HandleFunc("/welcome", b.setComonHeaders(b.welcomeFunc)).Methods("GET")
	s.HandleFunc("/room/{roomid}", b.setComonHeaders(b.parseRequestBody(b.roomHandler))).Methods("POST")
	s.HandleFunc("/stats", b.setComonHeaders(b.validateStatsRequest(b.statsHandler))).Methods("GET")

	// Expose prometheus metrics at "/metrics".
	r.HandleFunc("/metrics", b.setComonHeaders(b.validateStatsRequest(b.metricsHandler))).Methods("GET")

	// Provide a REST service to get TURN credentials.
	// See https://tools.ietf.org/html/draft-uberti-behave-turn-rest-00
	r.HandleFunc("/turn/credentials", b.setComonHeaders(b.getTurnCredentials)).Methods("GET")
	return nil
}

func (b *BackendServer) setComonHeaders(f func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "nextcloud-spreed-signaling/"+b.version)
		w.Header().Set("X-Spreed-Signaling-Features", strings.Join(b.hub.info.Features, ", "))
		f(w, r)
	}
}

func (b *BackendServer) welcomeFunc(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, b.welcomeMessage) // nolint
}

func calculateTurnSecret(username string, secret []byte, valid time.Duration) (string, string) {
	expires := time.Now().Add(valid)
	username = fmt.Sprintf("%d:%s", expires.Unix(), username)
	m := hmac.New(sha1.New, secret)
	m.Write([]byte(username)) // nolint
	password := base64.StdEncoding.EncodeToString(m.Sum(nil))
	return username, password
}

func (b *BackendServer) getTurnCredentials(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	service := q.Get("service")
	username := q.Get("username")
	key := q.Get("key")
	if key == "" {
		// The RFC actually defines "key" to be the parameter, but Janus sends it as "api".
		key = q.Get("api")
	}
	if service != "turn" || key == "" {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "Invalid service and/or key sent.\n") // nolint
		return
	}

	if key != b.turnapikey {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, "Not allowed to access this service.\n") // nolint
		return
	}

	if len(b.turnservers) == 0 {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, "No TURN servers available.\n") // nolint
		return
	}

	if username == "" {
		// Make sure to include an actual username in the credentials.
		username = newRandomString(randomUsernameLength)
	}

	username, password := calculateTurnSecret(username, b.turnsecret, b.turnvalid)
	result := TurnCredentials{
		Username: username,
		Password: password,
		TTL:      int64(b.turnvalid.Seconds()),
		URIs:     b.turnservers,
	}

	data, err := json.Marshal(result)
	if err != nil {
		log.Printf("Could not serialize TURN credentials: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "Could not serialize credentials.") // nolint
		return
	}

	if data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(data) // nolint
}

func (b *BackendServer) parseRequestBody(f func(http.ResponseWriter, *http.Request, []byte)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		// Sanity checks
		if r.ContentLength == -1 {
			http.Error(w, "Length required", http.StatusLengthRequired)
			return
		} else if r.ContentLength > maxBodySize {
			http.Error(w, "Request entity too large", http.StatusRequestEntityTooLarge)
			return
		}
		ct := r.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "application/json") {
			log.Printf("Received unsupported content-type: %s", ct)
			http.Error(w, "Unsupported Content-Type", http.StatusBadRequest)
			return
		}

		if r.Header.Get(HeaderBackendSignalingRandom) == "" ||
			r.Header.Get(HeaderBackendSignalingChecksum) == "" {
			http.Error(w, "Authentication check failed", http.StatusForbidden)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Println("Error reading body: ", err)
			http.Error(w, "Could not read body", http.StatusBadRequest)
			return
		}

		f(w, r, body)
	}
}

func (b *BackendServer) sendRoomInvite(roomid string, backend *Backend, userids []string, properties *json.RawMessage) {
	msg := &ServerMessage{
		Type: "event",
		Event: &EventServerMessage{
			Target: "roomlist",
			Type:   "invite",
			Invite: &RoomEventServerMessage{
				RoomId:     roomid,
				Properties: properties,
			},
		},
	}
	for _, userid := range userids {
		if err := b.nats.PublishMessage(GetSubjectForUserId(userid, backend), msg); err != nil {
			log.Printf("Could not publish room invite for user %s in backend %s: %s", userid, backend.Id(), err)
		}
	}
}

func (b *BackendServer) sendRoomDisinvite(roomid string, backend *Backend, reason string, userids []string, sessionids []string) {
	msg := &ServerMessage{
		Type: "event",
		Event: &EventServerMessage{
			Target: "roomlist",
			Type:   "disinvite",
			Disinvite: &RoomDisinviteEventServerMessage{
				RoomEventServerMessage: RoomEventServerMessage{
					RoomId: roomid,
				},
				Reason: reason,
			},
		},
	}
	for _, userid := range userids {
		if err := b.nats.PublishMessage(GetSubjectForUserId(userid, backend), msg); err != nil {
			log.Printf("Could not publish room disinvite for user %s in backend %s: %s", userid, backend.Id(), err)
		}
	}

	timeout := time.Second
	var wg sync.WaitGroup
	for _, sessionid := range sessionids {
		if sessionid == sessionIdNotInMeeting {
			// Ignore entries that are no longer in the meeting.
			continue
		}

		wg.Add(1)
		go func(sessionid string) {
			defer wg.Done()
			if sid, err := b.lookupByRoomSessionId(sessionid, nil, timeout); err != nil {
				log.Printf("Could not lookup by room session %s: %s", sessionid, err)
			} else if sid != "" {
				if err := b.nats.PublishMessage("session."+sid, msg); err != nil {
					log.Printf("Could not publish room disinvite for session %s: %s", sid, err)
				}
			}
		}(sessionid)
	}
	wg.Wait()
}

func (b *BackendServer) sendRoomUpdate(roomid string, backend *Backend, notified_userids []string, all_userids []string, properties *json.RawMessage) {
	msg := &ServerMessage{
		Type: "event",
		Event: &EventServerMessage{
			Target: "roomlist",
			Type:   "update",
			Update: &RoomEventServerMessage{
				RoomId:     roomid,
				Properties: properties,
			},
		},
	}
	notified := make(map[string]bool)
	for _, userid := range notified_userids {
		notified[userid] = true
	}
	// Only send to users not notified otherwise.
	for _, userid := range all_userids {
		if notified[userid] {
			continue
		}

		if err := b.nats.PublishMessage(GetSubjectForUserId(userid, backend), msg); err != nil {
			log.Printf("Could not publish room update for user %s in backend %s: %s", userid, backend.Id(), err)
		}
	}
}

func (b *BackendServer) lookupByRoomSessionId(roomSessionId string, cache *ConcurrentStringStringMap, timeout time.Duration) (string, error) {
	if roomSessionId == sessionIdNotInMeeting {
		log.Printf("Trying to lookup empty room session id: %s", roomSessionId)
		return "", nil
	}

	if cache != nil {
		if result, found := cache.Get(roomSessionId); found {
			return result, nil
		}
	}

	sid, err := b.roomSessions.GetSessionId(roomSessionId)
	if err == ErrNoSuchRoomSession {
		return "", nil
	} else if err != nil {
		return "", err
	}

	if cache != nil {
		cache.Set(roomSessionId, sid)
	}
	return sid, nil
}

func (b *BackendServer) fixupUserSessions(cache *ConcurrentStringStringMap, users []map[string]interface{}, timeout time.Duration) []map[string]interface{} {
	if len(users) == 0 {
		return users
	}

	var wg sync.WaitGroup
	for _, user := range users {
		roomSessionIdOb, found := user["sessionId"]
		if !found {
			continue
		}

		roomSessionId, ok := roomSessionIdOb.(string)
		if !ok {
			log.Printf("User %+v has invalid room session id, ignoring", user)
			delete(user, "sessionId")
			continue
		}

		if roomSessionId == sessionIdNotInMeeting {
			log.Printf("User %+v is not in the meeting, ignoring", user)
			delete(user, "sessionId")
			continue
		}

		wg.Add(1)
		go func(roomSessionId string, u map[string]interface{}) {
			defer wg.Done()
			if sessionId, err := b.lookupByRoomSessionId(roomSessionId, cache, timeout); err != nil {
				log.Printf("Could not lookup by room session %s: %s", roomSessionId, err)
				delete(u, "sessionId")
			} else if sessionId != "" {
				u["sessionId"] = sessionId
			} else {
				// sessionId == ""
				delete(u, "sessionId")
			}
		}(roomSessionId, user)
	}
	wg.Wait()

	result := make([]map[string]interface{}, 0, len(users))
	for _, user := range users {
		if _, found := user["sessionId"]; found {
			result = append(result, user)
		}
	}
	return result
}

func (b *BackendServer) sendRoomIncall(roomid string, backend *Backend, request *BackendServerRoomRequest) error {
	if !request.InCall.All {
		timeout := time.Second

		var cache ConcurrentStringStringMap
		// Convert (Nextcloud) session ids to signaling session ids.
		request.InCall.Users = b.fixupUserSessions(&cache, request.InCall.Users, timeout)
		// Entries in "Changed" are most likely already fetched through the "Users" list.
		request.InCall.Changed = b.fixupUserSessions(&cache, request.InCall.Changed, timeout)

		if len(request.InCall.Users) == 0 && len(request.InCall.Changed) == 0 {
			return nil
		}
	}

	return b.nats.PublishBackendServerRoomRequest(GetSubjectForBackendRoomId(roomid, backend), request)
}

func (b *BackendServer) sendRoomParticipantsUpdate(roomid string, backend *Backend, request *BackendServerRoomRequest) error {
	timeout := time.Second

	// Convert (Nextcloud) session ids to signaling session ids.
	var cache ConcurrentStringStringMap
	request.Participants.Users = b.fixupUserSessions(&cache, request.Participants.Users, timeout)
	request.Participants.Changed = b.fixupUserSessions(&cache, request.Participants.Changed, timeout)

	if len(request.Participants.Users) == 0 && len(request.Participants.Changed) == 0 {
		return nil
	}

	var wg sync.WaitGroup
loop:
	for _, user := range request.Participants.Changed {
		permissionsInterface, found := user["permissions"]
		if !found {
			continue
		}

		sessionId := user["sessionId"].(string)
		permissionsList, ok := permissionsInterface.([]interface{})
		if !ok {
			log.Printf("Received invalid permissions %+v (%s) for session %s", permissionsInterface, reflect.TypeOf(permissionsInterface), sessionId)
			continue
		}
		var permissions []Permission
		for idx, ob := range permissionsList {
			permission, ok := ob.(string)
			if !ok {
				log.Printf("Received invalid permission at position %d %+v (%s) for session %s", idx, ob, reflect.TypeOf(ob), sessionId)
				continue loop
			}
			permissions = append(permissions, Permission(permission))
		}
		wg.Add(1)

		go func(sessionId string, permissions []Permission) {
			defer wg.Done()
			message := &NatsMessage{
				Type:        "permissions",
				Permissions: permissions,
			}
			if err := b.nats.Publish("session."+sessionId, message); err != nil {
				log.Printf("Could not send permissions update (%+v) to session %s: %s", permissions, sessionId, err)
			}
		}(sessionId, permissions)
	}
	wg.Wait()

	return b.nats.PublishBackendServerRoomRequest(GetSubjectForBackendRoomId(roomid, backend), request)
}

func (b *BackendServer) sendRoomMessage(roomid string, backend *Backend, request *BackendServerRoomRequest) error {
	return b.nats.PublishBackendServerRoomRequest(GetSubjectForBackendRoomId(roomid, backend), request)
}

func (b *BackendServer) roomHandler(w http.ResponseWriter, r *http.Request, body []byte) {
	v := mux.Vars(r)
	roomid := v["roomid"]

	var backend *Backend
	backendUrl := r.Header.Get(HeaderBackendServer)
	if backendUrl != "" {
		if u, err := url.Parse(backendUrl); err == nil {
			backend = b.hub.backend.GetBackend(u)
		}

		if backend == nil {
			// Unknown backend URL passed, return immediately.
			http.Error(w, "Authentication check failed", http.StatusForbidden)
			return
		}
	}

	if backend == nil {
		if compatBackend := b.hub.backend.GetCompatBackend(); compatBackend != nil {
			// Old-style configuration using a single secret for all backends.
			backend = compatBackend
		} else {
			// Old-style Talk, find backend that created the checksum.
			// TODO(fancycode): Remove once all supported Talk versions send the backend header.
			for _, b := range b.hub.backend.GetBackends() {
				if ValidateBackendChecksum(r, body, b.Secret()) {
					backend = b
					break
				}
			}
		}

		if backend == nil {
			http.Error(w, "Authentication check failed", http.StatusForbidden)
			return
		}
	}

	if !ValidateBackendChecksum(r, body, backend.Secret()) {
		http.Error(w, "Authentication check failed", http.StatusForbidden)
		return
	}

	var request BackendServerRoomRequest
	if err := json.Unmarshal(body, &request); err != nil {
		log.Printf("Error decoding body %s: %s", string(body), err)
		http.Error(w, "Could not read body", http.StatusBadRequest)
		return
	}

	request.ReceivedTime = time.Now().UnixNano()

	var err error
	switch request.Type {
	case "invite":
		b.sendRoomInvite(roomid, backend, request.Invite.UserIds, request.Invite.Properties)
		b.sendRoomUpdate(roomid, backend, request.Invite.UserIds, request.Invite.AllUserIds, request.Invite.Properties)
	case "disinvite":
		b.sendRoomDisinvite(roomid, backend, DisinviteReasonDisinvited, request.Disinvite.UserIds, request.Disinvite.SessionIds)
		b.sendRoomUpdate(roomid, backend, request.Disinvite.UserIds, request.Disinvite.AllUserIds, request.Disinvite.Properties)
	case "update":
		err = b.nats.PublishBackendServerRoomRequest(GetSubjectForBackendRoomId(roomid, backend), &request)
		b.sendRoomUpdate(roomid, backend, nil, request.Update.UserIds, request.Update.Properties)
	case "delete":
		err = b.nats.PublishBackendServerRoomRequest(GetSubjectForBackendRoomId(roomid, backend), &request)
		b.sendRoomDisinvite(roomid, backend, DisinviteReasonDeleted, request.Delete.UserIds, nil)
	case "incall":
		err = b.sendRoomIncall(roomid, backend, &request)
	case "participants":
		err = b.sendRoomParticipantsUpdate(roomid, backend, &request)
	case "message":
		err = b.sendRoomMessage(roomid, backend, &request)
	default:
		http.Error(w, "Unsupported request type: "+request.Type, http.StatusBadRequest)
		return
	}

	if err != nil {
		log.Printf("Error processing %s for room %s: %s", string(body), roomid, err)
		http.Error(w, "Error while processing", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	// TODO(jojo): Return better response struct.
	w.Write([]byte("{}")) // nolint
}

func (b *BackendServer) validateStatsRequest(f func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		addr := getRealUserIP(r)
		if strings.Contains(addr, ":") {
			if host, _, err := net.SplitHostPort(addr); err == nil {
				addr = host
			}
		}
		if !b.statsAllowedIps[addr] {
			http.Error(w, "Authentication check failed", http.StatusForbidden)
			return
		}

		f(w, r)
	}
}

func (b *BackendServer) statsHandler(w http.ResponseWriter, r *http.Request) {
	stats := b.hub.GetStats()
	statsData, err := json.MarshalIndent(stats, "", "  ")
	if err != nil {
		log.Printf("Could not serialize stats %+v: %s", stats, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	w.Write(statsData) // nolint
}

func (b *BackendServer) metricsHandler(w http.ResponseWriter, r *http.Request) {
	promhttp.Handler().ServeHTTP(w, r)
}
