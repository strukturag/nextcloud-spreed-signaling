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
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

const (
	maxBodySize = 256 * 1024

	randomUsernameLength = 32

	sessionIdNotInMeeting = "0"
)

type BackendServer struct {
	log          *zap.Logger
	hub          *Hub
	events       AsyncEvents
	roomSessions RoomSessions

	version        string
	welcomeMessage string

	turnapikey  string
	turnsecret  []byte
	turnvalid   time.Duration
	turnservers []string

	statsAllowedIps atomic.Pointer[AllowedIps]
	invalidSecret   []byte
}

func NewBackendServer(log *zap.Logger, config *goconf.ConfigFile, hub *Hub, version string) (*BackendServer, error) {
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

		log.Info("Using configured TURN API key")
		log.Info("Using configured shared TURN secret")
		for _, s := range turnserverslist {
			log.Info("Adding TURN server",
				zap.String("server", s),
			)
		}
	}

	statsAllowed, _ := config.GetString("stats", "allowed_ips")
	statsAllowedIps, err := ParseAllowedIps(statsAllowed)
	if err != nil {
		return nil, err
	}

	if !statsAllowedIps.Empty() {
		log.Info("Access to the stats endpoint only allowed from ips",
			zap.Stringer("ips", statsAllowedIps),
		)
	} else {
		statsAllowedIps = DefaultAllowedIps()
		log.Info("No IPs configured for the stats endpoint, only allowing access from default ips",
			zap.Stringer("ips", statsAllowedIps),
		)
	}

	invalidSecret := make([]byte, 32)
	if _, err := rand.Read(invalidSecret); err != nil {
		return nil, err
	}

	result := &BackendServer{
		log:          log,
		hub:          hub,
		events:       hub.events,
		roomSessions: hub.roomSessions,
		version:      version,

		turnapikey:  turnapikey,
		turnsecret:  []byte(turnsecret),
		turnvalid:   turnvalid,
		turnservers: turnserverslist,

		invalidSecret: invalidSecret,
	}

	result.statsAllowedIps.Store(statsAllowedIps)

	return result, nil
}

func (b *BackendServer) Reload(config *goconf.ConfigFile) {
	statsAllowed, _ := config.GetString("stats", "allowed_ips")
	if statsAllowedIps, err := ParseAllowedIps(statsAllowed); err == nil {
		if !statsAllowedIps.Empty() {
			b.log.Info("Access to the stats endpoint only allowed from ips",
				zap.Stringer("ips", statsAllowedIps),
			)
		} else {
			statsAllowedIps = DefaultAllowedIps()
			b.log.Info("No IPs configured for the stats endpoint, only allowing access from default ips",
				zap.Stringer("ips", statsAllowedIps),
			)
		}
		b.statsAllowedIps.Store(statsAllowedIps)
	} else {
		b.log.Error("Error parsing allowed stats ips",
			zap.String("allowed", statsAllowed),
			zap.Error(err),
		)
	}
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
		b.log.Error("Could not serialize TURN credentials",
			zap.Error(err),
		)
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
			b.log.Warn("Received unsupported content-type",
				zap.String("contenttype", ct),
			)
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
			b.log.Error("Error reading body",
				zap.Error(err),
			)
			http.Error(w, "Could not read body", http.StatusBadRequest)
			return
		}

		f(w, r, body)
	}
}

func (b *BackendServer) sendRoomInvite(roomid string, backend *Backend, userids []string, properties json.RawMessage) {
	msg := &AsyncMessage{
		Type: "message",
		Message: &ServerMessage{
			Type: "event",
			Event: &EventServerMessage{
				Target: "roomlist",
				Type:   "invite",
				Invite: &RoomEventServerMessage{
					RoomId:     roomid,
					Properties: properties,
				},
			},
		},
	}
	log := b.log.With(
		zap.String("room", roomid),
		zap.String("backend", backend.Id()),
	)
	for _, userid := range userids {
		if err := b.events.PublishUserMessage(userid, backend, msg); err != nil {
			log.Error("Could not publish room invite",
				zap.String("userid", userid),
				zap.Error(err),
			)
		}
	}
}

func (b *BackendServer) sendRoomDisinvite(roomid string, backend *Backend, reason string, userids []string, sessionids []string) {
	msg := &AsyncMessage{
		Type: "message",
		Message: &ServerMessage{
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
		},
	}
	log := b.log.With(
		zap.String("room", roomid),
		zap.String("backend", backend.Id()),
	)
	for _, userid := range userids {
		if err := b.events.PublishUserMessage(userid, backend, msg); err != nil {
			log.Error("Could not publish room disinvite",
				zap.String("userid", userid),
				zap.Error(err),
			)
		}
	}

	timeout := time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var wg sync.WaitGroup
	for _, sessionid := range sessionids {
		if sessionid == sessionIdNotInMeeting {
			// Ignore entries that are no longer in the meeting.
			continue
		}

		wg.Add(1)
		go func(sessionid string) {
			defer wg.Done()
			if sid, err := b.lookupByRoomSessionId(ctx, sessionid, nil); err != nil {
				log.Error("Could not lookup by room session",
					zap.String("roomsessionid", sessionid),
					zap.Error(err),
				)
			} else if sid != "" {
				if err := b.events.PublishSessionMessage(sid, backend, msg); err != nil {
					log.Error("Could not publish room disinvite",
						zap.String("sessionid", sid),
						zap.Error(err),
					)
				}
			}
		}(sessionid)
	}
	wg.Wait()
}

func (b *BackendServer) sendRoomUpdate(roomid string, backend *Backend, notified_userids []string, all_userids []string, properties json.RawMessage) {
	msg := &AsyncMessage{
		Type: "message",
		Message: &ServerMessage{
			Type: "event",
			Event: &EventServerMessage{
				Target: "roomlist",
				Type:   "update",
				Update: &RoomEventServerMessage{
					RoomId:     roomid,
					Properties: properties,
				},
			},
		},
	}
	log := b.log.With(
		zap.String("room", roomid),
		zap.String("backend", backend.Id()),
	)
	notified := make(map[string]bool)
	for _, userid := range notified_userids {
		notified[userid] = true
	}
	// Only send to users not notified otherwise.
	for _, userid := range all_userids {
		if notified[userid] {
			continue
		}

		if err := b.events.PublishUserMessage(userid, backend, msg); err != nil {
			log.Error("Could not publish room update",
				zap.String("userid", userid),
				zap.Error(err),
			)
		}
	}
}

func (b *BackendServer) lookupByRoomSessionId(ctx context.Context, roomSessionId string, cache *ConcurrentStringStringMap) (string, error) {
	if roomSessionId == sessionIdNotInMeeting {
		b.log.Debug("Trying to lookup empty room session id",
			zap.String("roomsessionid", roomSessionId),
		)
		return "", nil
	}

	if cache != nil {
		if result, found := cache.Get(roomSessionId); found {
			return result, nil
		}
	}

	sid, err := b.roomSessions.LookupSessionId(ctx, roomSessionId, "")
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

func (b *BackendServer) fixupUserSessions(ctx context.Context, cache *ConcurrentStringStringMap, users []map[string]interface{}) []map[string]interface{} {
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
			b.log.Warn("User has invalid room session id, ignoring",
				zap.Any("user", user),
			)
			delete(user, "sessionId")
			continue
		}

		if roomSessionId == sessionIdNotInMeeting {
			b.log.Warn("User is not in the meeting, ignoring",
				zap.Any("user", user),
			)
			delete(user, "sessionId")
			continue
		}

		wg.Add(1)
		go func(roomSessionId string, u map[string]interface{}) {
			defer wg.Done()
			if sessionId, err := b.lookupByRoomSessionId(ctx, roomSessionId, cache); err != nil {
				b.log.Error("Could not lookup by room session",
					zap.String("roomsessionid", roomSessionId),
					zap.Error(err),
				)
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

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		var cache ConcurrentStringStringMap
		// Convert (Nextcloud) session ids to signaling session ids.
		request.InCall.Users = b.fixupUserSessions(ctx, &cache, request.InCall.Users)
		// Entries in "Changed" are most likely already fetched through the "Users" list.
		request.InCall.Changed = b.fixupUserSessions(ctx, &cache, request.InCall.Changed)

		if len(request.InCall.Users) == 0 && len(request.InCall.Changed) == 0 {
			return nil
		}
	}

	message := &AsyncMessage{
		Type: "room",
		Room: request,
	}
	return b.events.PublishBackendRoomMessage(roomid, backend, message)
}

func (b *BackendServer) sendRoomParticipantsUpdate(roomid string, backend *Backend, request *BackendServerRoomRequest) error {
	timeout := time.Second

	// Convert (Nextcloud) session ids to signaling session ids.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var cache ConcurrentStringStringMap
	request.Participants.Users = b.fixupUserSessions(ctx, &cache, request.Participants.Users)
	request.Participants.Changed = b.fixupUserSessions(ctx, &cache, request.Participants.Changed)

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
		log := b.log.With(
			zap.String("sessionid", sessionId),
		)
		permissionsList, ok := permissionsInterface.([]interface{})
		if !ok {
			log.Warn("Received invalid permissions for session, ignoring",
				zap.Any("permissions", permissionsInterface),
				zap.Any("type", reflect.TypeOf(permissionsInterface)),
			)
			continue
		}
		var permissions []Permission
		for idx, ob := range permissionsList {
			permission, ok := ob.(string)
			if !ok {
				log.Warn("Received invalid permission at position for session, ignoring",
					zap.Int("index", idx),
					zap.Any("permission", ob),
					zap.Any("type", reflect.TypeOf(ob)),
				)
				continue loop
			}
			permissions = append(permissions, Permission(permission))
		}
		wg.Add(1)

		go func(sessionId string, permissions []Permission) {
			defer wg.Done()
			message := &AsyncMessage{
				Type:        "permissions",
				Permissions: permissions,
			}
			if err := b.events.PublishSessionMessage(sessionId, backend, message); err != nil {
				log.Error("Could not send permissions update",
					zap.Any("permissions", permissions),
					zap.Error(err),
				)
			}
		}(sessionId, permissions)
	}
	wg.Wait()

	message := &AsyncMessage{
		Type: "room",
		Room: request,
	}
	return b.events.PublishBackendRoomMessage(roomid, backend, message)
}

func (b *BackendServer) sendRoomMessage(roomid string, backend *Backend, request *BackendServerRoomRequest) error {
	message := &AsyncMessage{
		Type: "room",
		Room: request,
	}
	return b.events.PublishBackendRoomMessage(roomid, backend, message)
}

func (b *BackendServer) sendRoomSwitchTo(roomid string, backend *Backend, request *BackendServerRoomRequest) error {
	timeout := time.Second

	// Convert (Nextcloud) session ids to signaling session ids.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex
	if len(request.SwitchTo.Sessions) > 0 {
		// We support both a list of sessions or a map with additional details per session.
		if request.SwitchTo.Sessions[0] == '[' {
			var sessionsList BackendRoomSwitchToSessionsList
			if err := json.Unmarshal(request.SwitchTo.Sessions, &sessionsList); err != nil {
				return err
			}

			if len(sessionsList) == 0 {
				return nil
			}

			var internalSessionsList BackendRoomSwitchToSessionsList
			for _, roomSessionId := range sessionsList {
				if roomSessionId == sessionIdNotInMeeting {
					continue
				}

				wg.Add(1)
				go func(roomSessionId string) {
					defer wg.Done()
					if sessionId, err := b.lookupByRoomSessionId(ctx, roomSessionId, nil); err != nil {
						b.log.Error("Could not lookup by room session",
							zap.String("roomsessionid", roomSessionId),
							zap.Error(err),
						)
					} else if sessionId != "" {
						mu.Lock()
						defer mu.Unlock()
						internalSessionsList = append(internalSessionsList, sessionId)
					}
				}(roomSessionId)
			}
			wg.Wait()

			mu.Lock()
			defer mu.Unlock()
			if len(internalSessionsList) == 0 {
				return nil
			}

			request.SwitchTo.SessionsList = internalSessionsList
			request.SwitchTo.SessionsMap = nil
		} else {
			var sessionsMap BackendRoomSwitchToSessionsMap
			if err := json.Unmarshal(request.SwitchTo.Sessions, &sessionsMap); err != nil {
				return err
			}

			if len(sessionsMap) == 0 {
				return nil
			}

			internalSessionsMap := make(BackendRoomSwitchToSessionsMap)
			for roomSessionId, details := range sessionsMap {
				if roomSessionId == sessionIdNotInMeeting {
					continue
				}

				wg.Add(1)
				go func(roomSessionId string, details json.RawMessage) {
					defer wg.Done()
					if sessionId, err := b.lookupByRoomSessionId(ctx, roomSessionId, nil); err != nil {
						b.log.Error("Could not lookup by room session",
							zap.String("roomsessionid", roomSessionId),
							zap.Error(err),
						)
					} else if sessionId != "" {
						mu.Lock()
						defer mu.Unlock()
						internalSessionsMap[sessionId] = details
					}
				}(roomSessionId, details)
			}
			wg.Wait()

			mu.Lock()
			defer mu.Unlock()
			if len(internalSessionsMap) == 0 {
				return nil
			}

			request.SwitchTo.SessionsList = nil
			request.SwitchTo.SessionsMap = internalSessionsMap
		}
	}
	request.SwitchTo.Sessions = nil

	message := &AsyncMessage{
		Type: "room",
		Room: request,
	}
	return b.events.PublishBackendRoomMessage(roomid, backend, message)
}

type BackendResponseWithStatus interface {
	Status() int
}

type DialoutErrorResponse struct {
	BackendServerRoomResponse

	status int
}

func (r *DialoutErrorResponse) Status() int {
	return r.status
}

func returnDialoutError(status int, err *Error) (any, error) {
	response := &DialoutErrorResponse{
		BackendServerRoomResponse: BackendServerRoomResponse{
			Type: "dialout",
			Dialout: &BackendRoomDialoutResponse{
				Error: err,
			},
		},

		status: status,
	}
	return response, nil
}

var checkNumeric = regexp.MustCompile(`^[0-9]+$`)

func isNumeric(s string) bool {
	return checkNumeric.MatchString(s)
}

func (b *BackendServer) startDialout(roomid string, backend *Backend, backendUrl string, request *BackendServerRoomRequest) (any, error) {
	if err := request.Dialout.ValidateNumber(); err != nil {
		return returnDialoutError(http.StatusBadRequest, err)
	}

	if !isNumeric(roomid) {
		return returnDialoutError(http.StatusBadRequest, NewError("invalid_roomid", "The room id must be numeric."))
	}

	session := b.hub.GetDialoutSession(roomid, backend)
	if session == nil {
		return returnDialoutError(http.StatusNotFound, NewError("no_client_available", "No available client found to trigger dialout."))
	}

	url := backend.Url()
	if url == "" {
		// Old-style compat backend, use client-provided URL.
		url = backendUrl
		if url != "" && url[len(url)-1] != '/' {
			url += "/"
		}
	}
	id := newRandomString(32)
	msg := &ServerMessage{
		Id:   id,
		Type: "internal",
		Internal: &InternalServerMessage{
			Type: "dialout",
			Dialout: &InternalServerDialoutRequest{
				RoomId:  roomid,
				Backend: url,
				Request: request.Dialout,
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var response atomic.Pointer[DialoutInternalClientMessage]

	session.HandleResponse(id, func(message *ClientMessage) bool {
		response.Store(message.Internal.Dialout)
		cancel()
		// Don't send error to other sessions in the room.
		return message.Internal.Dialout.Error != nil
	})
	defer session.ClearResponseHandler(id)

	if !session.SendMessage(msg) {
		return returnDialoutError(http.StatusBadGateway, NewError("error_notify", "Could not notify about new dialout."))
	}

	<-ctx.Done()
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return returnDialoutError(http.StatusGatewayTimeout, NewError("timeout", "Timeout while waiting for dialout to start."))
	}

	dialout := response.Load()
	if dialout == nil {
		return returnDialoutError(http.StatusBadGateway, NewError("error_notify", "No dialout response received."))
	}

	switch dialout.Type {
	case "error":
		return returnDialoutError(http.StatusBadGateway, dialout.Error)
	case "status":
		if dialout.Status.Status != DialoutStatusAccepted {
			b.log.Warn("Received unsupported dialout status when triggering dialout",
				zap.Any("status", dialout),
			)
			return returnDialoutError(http.StatusBadGateway, NewError("unsupported_status", "Unsupported dialout status received."))
		}

		return &BackendServerRoomResponse{
			Type: "dialout",
			Dialout: &BackendRoomDialoutResponse{
				CallId: dialout.Status.CallId,
			},
		}, nil
	}

	b.log.Warn("Received unsupported dialout type when triggering dialout",
		zap.Any("status", dialout),
	)
	return returnDialoutError(http.StatusBadGateway, NewError("unsupported_type", "Unsupported dialout type received."))
}

func (b *BackendServer) roomHandler(w http.ResponseWriter, r *http.Request, body []byte) {
	throttle, err := b.hub.throttler.CheckBruteforce(r.Context(), b.hub.getRealUserIP(r), "BackendRoomAuth")
	if err == ErrBruteforceDetected {
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	} else if err != nil {
		b.log.Error("Error checking for bruteforce",
			zap.Error(err),
		)
		http.Error(w, "Could not check for bruteforce", http.StatusInternalServerError)
		return
	}

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
			throttle(r.Context())
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
			throttle(r.Context())
			http.Error(w, "Authentication check failed", http.StatusForbidden)
			return
		}
	}

	if !ValidateBackendChecksum(r, body, backend.Secret()) {
		throttle(r.Context())
		http.Error(w, "Authentication check failed", http.StatusForbidden)
		return
	}

	var request BackendServerRoomRequest
	if err := json.Unmarshal(body, &request); err != nil {
		b.log.Error("Error decoding body",
			zap.Binary("body", body),
			zap.Error(err),
		)
		http.Error(w, "Could not read body", http.StatusBadRequest)
		return
	}

	request.ReceivedTime = time.Now().UnixNano()

	var response any
	switch request.Type {
	case "invite":
		b.sendRoomInvite(roomid, backend, request.Invite.UserIds, request.Invite.Properties)
		b.sendRoomUpdate(roomid, backend, request.Invite.UserIds, request.Invite.AllUserIds, request.Invite.Properties)
	case "disinvite":
		b.sendRoomDisinvite(roomid, backend, DisinviteReasonDisinvited, request.Disinvite.UserIds, request.Disinvite.SessionIds)
		b.sendRoomUpdate(roomid, backend, request.Disinvite.UserIds, request.Disinvite.AllUserIds, request.Disinvite.Properties)
	case "update":
		message := &AsyncMessage{
			Type: "room",
			Room: &request,
		}
		err = b.events.PublishBackendRoomMessage(roomid, backend, message)
		b.sendRoomUpdate(roomid, backend, nil, request.Update.UserIds, request.Update.Properties)
	case "delete":
		message := &AsyncMessage{
			Type: "room",
			Room: &request,
		}
		err = b.events.PublishBackendRoomMessage(roomid, backend, message)
		b.sendRoomDisinvite(roomid, backend, DisinviteReasonDeleted, request.Delete.UserIds, nil)
	case "incall":
		err = b.sendRoomIncall(roomid, backend, &request)
	case "participants":
		err = b.sendRoomParticipantsUpdate(roomid, backend, &request)
	case "message":
		err = b.sendRoomMessage(roomid, backend, &request)
	case "switchto":
		err = b.sendRoomSwitchTo(roomid, backend, &request)
	case "dialout":
		response, err = b.startDialout(roomid, backend, backendUrl, &request)
	default:
		http.Error(w, "Unsupported request type: "+request.Type, http.StatusBadRequest)
		return
	}

	if err != nil {
		b.log.Error("Error processing room request",
			zap.ByteString("request", body),
			zap.String("room", roomid),
			zap.Error(err),
		)
		http.Error(w, "Error while processing", http.StatusInternalServerError)
		return
	}

	var responseData []byte
	responseStatus := http.StatusOK
	if response == nil {
		// TODO(jojo): Return better response struct.
		responseData = []byte("{}")
	} else {
		if s, ok := response.(BackendResponseWithStatus); ok {
			responseStatus = s.Status()
		}
		responseData, err = json.Marshal(response)
		if err != nil {
			b.log.Error("Could not serialize backend response",
				zap.Any("response", response),
				zap.Error(err),
			)
			responseStatus = http.StatusInternalServerError
			responseData = []byte("{\"error\":\"could_not_serialize\"}")
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(responseStatus)
	w.Write(responseData) // nolint
}

func (b *BackendServer) allowStatsAccess(r *http.Request) bool {
	addr := b.hub.getRealUserIP(r)
	ip := net.ParseIP(addr)
	if len(ip) == 0 {
		return false
	}

	allowed := b.statsAllowedIps.Load()
	return allowed != nil && allowed.Allowed(ip)
}

func (b *BackendServer) validateStatsRequest(f func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if !b.allowStatsAccess(r) {
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
		b.log.Error("Could not serialize stats",
			zap.Any("stats", stats),
			zap.Error(err),
		)
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
