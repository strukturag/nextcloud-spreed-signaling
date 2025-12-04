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
	"net/http/pprof"
	"net/url"
	"reflect"
	"regexp"
	runtimepprof "runtime/pprof"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlintw/goconf"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
)

const (
	maxBodySize = 256 * 1024

	randomUsernameLength = 32

	sessionIdNotInMeeting = RoomSessionId("0")

	startDialoutTimeout = 45 * time.Second
)

type BackendServer struct {
	logger       Logger
	hub          *Hub
	events       AsyncEvents
	roomSessions RoomSessions

	version        string
	debug          bool
	welcomeMessage string

	turnapikey  string
	turnsecret  []byte
	turnvalid   time.Duration
	turnservers []string

	statsAllowedIps atomic.Pointer[AllowedIps]
	invalidSecret   []byte

	buffers BufferPool
}

func NewBackendServer(ctx context.Context, config *goconf.ConfigFile, hub *Hub, version string) (*BackendServer, error) {
	logger := LoggerFromContext(ctx)
	turnapikey, _ := GetStringOptionWithEnv(config, "turn", "apikey")
	turnsecret, _ := GetStringOptionWithEnv(config, "turn", "secret")
	turnservers, _ := config.GetString("turn", "servers")
	// TODO(jojo): Make the validity for TURN credentials configurable.
	turnvalid := 24 * time.Hour

	turnserverslist := slices.Collect(SplitEntries(turnservers, ","))
	if len(turnserverslist) != 0 {
		if turnapikey == "" {
			return nil, fmt.Errorf("need a TURN API key if TURN servers are configured")
		}
		if turnsecret == "" {
			return nil, fmt.Errorf("need a shared TURN secret if TURN servers are configured")
		}

		logger.Printf("Using configured TURN API key")
		logger.Printf("Using configured shared TURN secret")
		for _, s := range turnserverslist {
			logger.Printf("Adding \"%s\" as TURN server", s)
		}
	}

	statsAllowed, _ := config.GetString("stats", "allowed_ips")
	statsAllowedIps, err := ParseAllowedIps(statsAllowed)
	if err != nil {
		return nil, err
	}

	if !statsAllowedIps.Empty() {
		logger.Printf("Only allowing access to the stats endpoint from %s", statsAllowed)
	} else {
		statsAllowedIps = DefaultAllowedIps()
		logger.Printf("No IPs configured for the stats endpoint, only allowing access from %s", statsAllowedIps)
	}

	invalidSecret := make([]byte, 32)
	if _, err := rand.Read(invalidSecret); err != nil {
		return nil, err
	}

	debug, _ := config.GetBool("app", "debug")

	result := &BackendServer{
		logger:       logger,
		hub:          hub,
		events:       hub.events,
		roomSessions: hub.roomSessions,
		version:      version,
		debug:        debug,

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
			b.logger.Printf("Only allowing access to the stats endpoint from %s", statsAllowed)
		} else {
			statsAllowedIps = DefaultAllowedIps()
			b.logger.Printf("No IPs configured for the stats endpoint, only allowing access from %s", statsAllowedIps)
		}
		b.statsAllowedIps.Store(statsAllowedIps)
	} else {
		b.logger.Printf("Error parsing allowed stats ips from \"%s\": %s", statsAllowedIps, err)
	}
}

func (b *BackendServer) Start(r *mux.Router) error {
	welcome := map[string]string{
		"nextcloud-spreed-signaling": "Welcome",
		"version":                    b.version,
	}
	welcomeMessage, _ := json.Marshal(welcome)
	b.welcomeMessage = string(welcomeMessage) + "\n"

	if b.debug {
		b.logger.Println("Installing debug handlers in \"/debug/pprof\"")
		s := r.PathPrefix("/debug/pprof").Subrouter()
		s.HandleFunc("", b.setCommonHeaders(b.validateStatsRequest(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/debug/pprof/", http.StatusTemporaryRedirect)
		})))
		s.HandleFunc("/", b.setCommonHeaders(b.validateStatsRequest(pprof.Index)))
		s.HandleFunc("/cmdline", b.setCommonHeaders(b.validateStatsRequest(pprof.Cmdline)))
		s.HandleFunc("/profile", b.setCommonHeaders(b.validateStatsRequest(pprof.Profile)))
		s.HandleFunc("/symbol", b.setCommonHeaders(b.validateStatsRequest(pprof.Symbol)))
		s.HandleFunc("/trace", b.setCommonHeaders(b.validateStatsRequest(pprof.Trace)))
		for _, profile := range runtimepprof.Profiles() {
			name := profile.Name()
			handler := pprof.Handler(name)
			s.HandleFunc("/"+name, b.setCommonHeaders(b.validateStatsRequest(func(w http.ResponseWriter, r *http.Request) {
				handler.ServeHTTP(w, r)
			})))
		}
	}

	s := r.PathPrefix("/api/v1").Subrouter()
	s.HandleFunc("/welcome", b.setCommonHeaders(b.welcomeFunc)).Methods("GET")
	s.HandleFunc("/room/{roomid}", b.setCommonHeaders(b.parseRequestBody(b.roomHandler))).Methods("POST")
	s.HandleFunc("/stats", b.setCommonHeaders(b.validateStatsRequest(b.statsHandler))).Methods("GET")
	s.HandleFunc("/serverinfo", b.setCommonHeaders(b.validateStatsRequest(b.serverinfoHandler))).Methods("GET")

	// Expose prometheus metrics at "/metrics".
	r.HandleFunc("/metrics", b.setCommonHeaders(b.validateStatsRequest(b.metricsHandler))).Methods("GET")

	// Provide a REST service to get TURN credentials.
	// See https://tools.ietf.org/html/draft-uberti-behave-turn-rest-00
	r.HandleFunc("/turn/credentials", b.setCommonHeaders(b.getTurnCredentials)).Methods("GET")
	return nil
}

func (b *BackendServer) setCommonHeaders(f func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
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
		b.logger.Printf("Could not serialize TURN credentials: %s", err)
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

func (b *BackendServer) parseRequestBody(f func(context.Context, http.ResponseWriter, *http.Request, []byte)) func(http.ResponseWriter, *http.Request) {
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
			b.logger.Printf("Received unsupported content-type: %s", ct)
			http.Error(w, "Unsupported Content-Type", http.StatusBadRequest)
			return
		}

		if r.Header.Get(HeaderBackendSignalingRandom) == "" ||
			r.Header.Get(HeaderBackendSignalingChecksum) == "" {
			http.Error(w, "Authentication check failed", http.StatusForbidden)
			return
		}

		body, err := b.buffers.ReadAll(r.Body)
		if err != nil {
			b.logger.Println("Error reading body: ", err)
			http.Error(w, "Could not read body", http.StatusBadRequest)
			return
		}
		defer b.buffers.Put(body)

		ctx := NewLoggerContext(r.Context(), b.logger)
		f(ctx, w, r, body.Bytes())
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
	for _, userid := range userids {
		if err := b.events.PublishUserMessage(userid, backend, msg); err != nil {
			b.logger.Printf("Could not publish room invite for user %s in backend %s: %s", userid, backend.Id(), err)
		}
	}
}

func (b *BackendServer) sendRoomDisinvite(roomid string, backend *Backend, reason string, userids []string, sessionids []RoomSessionId) {
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
	for _, userid := range userids {
		if err := b.events.PublishUserMessage(userid, backend, msg); err != nil {
			b.logger.Printf("Could not publish room disinvite for user %s in backend %s: %s", userid, backend.Id(), err)
		}
	}

	timeout := time.Second
	ctx := NewLoggerContext(context.Background(), b.logger)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var wg sync.WaitGroup
	for _, sessionid := range sessionids {
		if sessionid == sessionIdNotInMeeting {
			// Ignore entries that are no longer in the meeting.
			continue
		}

		wg.Add(1)
		go func(sessionid RoomSessionId) {
			defer wg.Done()
			if sid, err := b.lookupByRoomSessionId(ctx, sessionid, nil); err != nil {
				b.logger.Printf("Could not lookup by room session %s: %s", sessionid, err)
			} else if sid != "" {
				if err := b.events.PublishSessionMessage(sid, backend, msg); err != nil {
					b.logger.Printf("Could not publish room disinvite for session %s: %s", sid, err)
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
			b.logger.Printf("Could not publish room update for user %s in backend %s: %s", userid, backend.Id(), err)
		}
	}
}

func (b *BackendServer) lookupByRoomSessionId(ctx context.Context, roomSessionId RoomSessionId, cache *ConcurrentMap[RoomSessionId, PublicSessionId]) (PublicSessionId, error) {
	if roomSessionId == sessionIdNotInMeeting {
		b.logger.Printf("Trying to lookup empty room session id: %s", roomSessionId)
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

func (b *BackendServer) fixupUserSessions(ctx context.Context, cache *ConcurrentMap[RoomSessionId, PublicSessionId], users []api.StringMap) []api.StringMap {
	if len(users) == 0 {
		return users
	}

	var wg sync.WaitGroup
	for _, user := range users {
		roomSessionId, found := api.GetStringMapString[RoomSessionId](user, "sessionId")
		if !found {
			b.logger.Printf("User %+v has invalid room session id, ignoring", user)
			delete(user, "sessionId")
			continue
		}

		if roomSessionId == sessionIdNotInMeeting {
			b.logger.Printf("User %+v is not in the meeting, ignoring", user)
			delete(user, "sessionId")
			continue
		}

		wg.Add(1)
		go func(roomSessionId RoomSessionId, u api.StringMap) {
			defer wg.Done()
			if sessionId, err := b.lookupByRoomSessionId(ctx, roomSessionId, cache); err != nil {
				b.logger.Printf("Could not lookup by room session %s: %s", roomSessionId, err)
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

	result := make([]api.StringMap, 0, len(users))
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

		ctx := NewLoggerContext(context.Background(), b.logger)
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var cache ConcurrentMap[RoomSessionId, PublicSessionId]
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

func (b *BackendServer) sendRoomParticipantsUpdate(ctx context.Context, roomid string, backend *Backend, request *BackendServerRoomRequest) error {
	timeout := time.Second

	// Convert (Nextcloud) session ids to signaling session ids.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var cache ConcurrentMap[RoomSessionId, PublicSessionId]
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

		sessionId, found := api.GetStringMapString[PublicSessionId](user, "sessionId")
		if !found {
			b.logger.Printf("User entry has no session id: %+v", user)
			continue
		}

		permissionsList, ok := permissionsInterface.([]any)
		if !ok {
			b.logger.Printf("Received invalid permissions %+v (%s) for session %s", permissionsInterface, reflect.TypeOf(permissionsInterface), sessionId)
			continue
		}
		var permissions []Permission
		for idx, ob := range permissionsList {
			permission, ok := ob.(string)
			if !ok {
				b.logger.Printf("Received invalid permission at position %d %+v (%s) for session %s", idx, ob, reflect.TypeOf(ob), sessionId)
				continue loop
			}
			permissions = append(permissions, Permission(permission))
		}
		wg.Add(1)

		go func(sessionId PublicSessionId, permissions []Permission) {
			defer wg.Done()
			message := &AsyncMessage{
				Type:        "permissions",
				Permissions: permissions,
			}
			if err := b.events.PublishSessionMessage(sessionId, backend, message); err != nil {
				b.logger.Printf("Could not send permissions update (%+v) to session %s: %s", permissions, sessionId, err)
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

func (b *BackendServer) sendRoomSwitchTo(ctx context.Context, roomid string, backend *Backend, request *BackendServerRoomRequest) error {
	timeout := time.Second

	// Convert (Nextcloud) session ids to signaling session ids.
	ctx, cancel := context.WithTimeout(ctx, timeout)
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

			var internalSessionsList BackendRoomSwitchToPublicSessionsList
			for _, roomSessionId := range sessionsList {
				if roomSessionId == sessionIdNotInMeeting {
					continue
				}

				wg.Add(1)
				go func(roomSessionId RoomSessionId) {
					defer wg.Done()
					if sessionId, err := b.lookupByRoomSessionId(ctx, roomSessionId, nil); err != nil {
						b.logger.Printf("Could not lookup by room session %s: %s", roomSessionId, err)
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

			internalSessionsMap := make(BackendRoomSwitchToPublicSessionsMap)
			for roomSessionId, details := range sessionsMap {
				if roomSessionId == sessionIdNotInMeeting {
					continue
				}

				wg.Add(1)
				go func(roomSessionId RoomSessionId, details json.RawMessage) {
					defer wg.Done()
					if sessionId, err := b.lookupByRoomSessionId(ctx, roomSessionId, nil); err != nil {
						b.logger.Printf("Could not lookup by room session %s: %s", roomSessionId, err)
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

func (b *BackendServer) startDialoutInSession(ctx context.Context, session *ClientSession, roomid string, backend *Backend, backendUrl string, request *BackendServerRoomRequest) (any, error) {
	url := backendUrl
	if url != "" && url[len(url)-1] != '/' {
		url += "/"
	}
	if urls := backend.Urls(); len(urls) > 0 {
		// Check if client-provided URL is registered for backend and use that.
		if !slices.ContainsFunc(urls, func(u string) bool {
			return strings.HasPrefix(url, u)
		}) {
			url = urls[0]
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

	subCtx, cancel := context.WithTimeout(ctx, startDialoutTimeout)
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
		return nil, NewError("error_notify", "Could not notify about new dialout.")
	}

	<-subCtx.Done()
	if err := subCtx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, NewError("timeout", "Timeout while waiting for dialout to start.")
		} else if errors.Is(err, context.Canceled) && errors.Is(ctx.Err(), context.Canceled) {
			// Upstream request was cancelled.
			return nil, err
		}
	}

	dialout := response.Load()
	if dialout == nil {
		return nil, NewError("error_notify", "No dialout response received.")
	}

	switch dialout.Type {
	case "error":
		return nil, dialout.Error
	case "status":
		if dialout.Status.Status != DialoutStatusAccepted {
			return nil, NewError("unsupported_status", fmt.Sprintf("Unsupported dialout status received: %+v", dialout))
		}

		return &BackendServerRoomResponse{
			Type: "dialout",
			Dialout: &BackendRoomDialoutResponse{
				CallId: dialout.Status.CallId,
			},
		}, nil
	default:
		return nil, NewError("unsupported_type", fmt.Sprintf("Unsupported dialout type received: %+v", dialout))
	}
}

func (b *BackendServer) startDialout(ctx context.Context, roomid string, backend *Backend, backendUrl string, request *BackendServerRoomRequest) (any, error) {
	if err := request.Dialout.ValidateNumber(); err != nil {
		return returnDialoutError(http.StatusBadRequest, err)
	}

	if !isNumeric(roomid) {
		return returnDialoutError(http.StatusBadRequest, NewError("invalid_roomid", "The room id must be numeric."))
	}

	var sessionError *Error
	sessions := b.hub.GetDialoutSessions(roomid, backend)
	for _, session := range sessions {
		if ctx.Err() != nil {
			// Upstream request was cancelled.
			break
		}

		response, err := b.startDialoutInSession(ctx, session, roomid, backend, backendUrl, request)
		if err != nil {
			b.logger.Printf("Error starting dialout request %+v in session %s: %+v", request.Dialout, session.PublicId(), err)
			var e *Error
			if sessionError == nil && errors.As(err, &e) {
				sessionError = e
			}
			continue
		}

		return response, nil
	}

	if sessionError != nil {
		return returnDialoutError(http.StatusBadGateway, sessionError)
	}

	return returnDialoutError(http.StatusNotFound, NewError("no_client_available", "No available client found to trigger dialout."))
}

func (b *BackendServer) roomHandler(ctx context.Context, w http.ResponseWriter, r *http.Request, body []byte) {
	throttle, err := b.hub.throttler.CheckBruteforce(ctx, b.hub.getRealUserIP(r), "BackendRoomAuth")
	if err == ErrBruteforceDetected {
		http.Error(w, "Too many requests", http.StatusTooManyRequests)
		return
	} else if err != nil {
		b.logger.Printf("Error checking for bruteforce: %s", err)
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
			throttle(ctx)
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
			throttle(ctx)
			http.Error(w, "Authentication check failed", http.StatusForbidden)
			return
		}
	}

	if !ValidateBackendChecksum(r, body, backend.Secret()) {
		throttle(ctx)
		http.Error(w, "Authentication check failed", http.StatusForbidden)
		return
	}

	var request BackendServerRoomRequest
	if err := json.Unmarshal(body, &request); err != nil {
		b.logger.Printf("Error decoding body %s: %s", string(body), err)
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
		err = b.sendRoomParticipantsUpdate(ctx, roomid, backend, &request)
	case "message":
		err = b.sendRoomMessage(roomid, backend, &request)
	case "switchto":
		err = b.sendRoomSwitchTo(ctx, roomid, backend, &request)
	case "dialout":
		response, err = b.startDialout(ctx, roomid, backend, backendUrl, &request)
	default:
		http.Error(w, "Unsupported request type: "+request.Type, http.StatusBadRequest)
		return
	}

	if err != nil {
		b.logger.Printf("Error processing %s for room %s: %s", string(body), roomid, err)
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
			b.logger.Printf("Could not serialize backend response %+v: %s", response, err)
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
		b.logger.Printf("Could not serialize stats %+v: %s", stats, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	w.Write(statsData) // nolint
}

func (b *BackendServer) serverinfoHandler(w http.ResponseWriter, r *http.Request) {
	info := BackendServerInfo{
		Version:  b.version,
		Features: b.hub.info.Features,

		Dialout: b.hub.GetServerInfoDialout(),
	}
	if mcu := b.hub.mcu; mcu != nil {
		info.Sfu = mcu.GetServerInfoSfu()
	}
	if e, ok := b.events.(*asyncEventsNats); ok {
		info.Nats = e.GetServerInfoNats()
	}
	if rpcClients := b.hub.rpcClients; rpcClients != nil {
		info.Grpc = rpcClients.GetServerInfoGrpc()
	}
	if etcdClient := b.hub.etcdClient; etcdClient != nil {
		info.Etcd = etcdClient.GetServerInfoEtcd()
	}

	infoData, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		b.logger.Printf("Could not serialize server info %+v: %s", info, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	w.Write(infoData) // nolint
}

func (b *BackendServer) metricsHandler(w http.ResponseWriter, r *http.Request) {
	promhttp.Handler().ServeHTTP(w, r)
}
