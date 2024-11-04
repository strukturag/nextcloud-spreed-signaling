# External signaling API

This document gives a rough overview on the API version 1.0 of the Spreed
signaling server. Clients can use the signaling server to send realtime
messages between different users / sessions.

The API describes the various messages that can be sent by a client or the
server to join rooms or distribute events between clients.

Depending on the server implementation, clients can use WebSockets (preferred)
or COMET (i.e. long-polling) requests to communicate with the signaling server.

For WebSockets, only the API described in this document is necessary. For COMET,
an extension to this API is required to identify a (virtual) connection between
multiple requests. The payload for COMET is the messages as described below.

See https://nextcloud-talk.readthedocs.io/en/latest/internal-signaling/ for
the API of the regular PHP backend.


## Request

    {
      "id": "unique-request-id",
      "type": "the-request-type",
      "the-request-type": {
        ...object defining the request...
      }
    }

Example:

    {
      "id": "123-abc",
      "type": "samplemessage",
      "samplemessage": {
        "foo": "bar",
        "baz": 1234
      }
    }


## Response

    {
      "id": "unique-request-id-from-request-if-present",
      "type": "the-response-type",
      "the-response-type": {
        ...object defining the response...
      }
    }

Example:

    {
      "id": "123-abc",
      "type": "sampleresponse",
      "sampleresponse": {
        "hello": "world!"
      }
    }


## Errors

The server can send error messages as a response to any request the client has
sent.

Message format:

    {
      "id": "unique-request-id-from-request-if-present",
      "type": "error",
      "error": {
        "code": "the-internal-message-id",
        "message": "human-readable-error-message",
        "details": {
          ...optional additional details...
        }
      }
    }


## Backend requests

For some messages, the signaling server has to perform a request to the
Nextcloud backend (e.g. to validate the user authentication). The backend
must be able to verify the request to make sure it is coming from a valid
signaling server.

Also the Nextcloud backend can send requests to the signaling server to notify
about events related to a room or user (e.g. a user is no longer invited to
a room). Here the signaling server must be able to verify the request to check
if it is coming from a valid Nextcloud instance.

Therefore all backend requests, either from the signaling server or vice versa
must contain two additional HTTP headers:

- `Spreed-Signaling-Random`: Random string of at least 32 bytes.
- `Spreed-Signaling-Checksum`: SHA256-HMAC of the random string and the request
  body, calculated with a shared secret. The shared secret is configured on
  both sides, so the checksum can be verified.
- `Spreed-Signaling-Backend`: Base URL of the Nextcloud server performing the
  request.

### Example

- Request body: `{"type":"auth","auth":{"version":"1.0","params":{"hello":"world"}}}`
- Random: `afb6b872ab03e3376b31bf0af601067222ff7990335ca02d327071b73c0119c6`
- Shared secret: `MySecretValue`
- Calculated checksum: `3c4a69ff328299803ac2879614b707c807b4758cf19450755c60656cac46e3bc`


## Welcome message

When a client connects, the server will immediately send a `welcome` message to
notify the client about supported features. This is available if the server
supports the `welcome` feature id.

Message format (Server -> Client):

    {
      "type": "welcome",
      "welcome": {
        "features": ["optional", "list, "of", "feature", "ids"],
        ...additional information about the server...
      }
    }


## Establish connection

This must be the first request by a newly connected client and is used to
authenticate the connection. No other messages can be sent without a successful
`hello` handshake.

Message format (Client -> Server):

    {
      "id": "unique-request-id",
      "type": "hello",
      "hello": {
        "version": "the-protocol-version",
        "features": ["optional", "list, "of", "client", "feature", "ids"],
        "auth": {
          "url": "the-url-to-the-auth-backend",
          "params": {
            ...object containing auth params...
          }
        }
      }
    }

Message format (Server -> Client):

    {
      "id": "unique-request-id-from-request",
      "type": "hello",
      "hello": {
        "sessionid": "the-unique-session-id",
        "resumeid": "the-unique-resume-id",
        "userid": "the-user-id-for-known-users",
        "version": "the-protocol-version",
        "server": {
          "features": ["optional", "list, "of", "feature", "ids"],
          ...additional information about the server...
        }
      }
    }

Please note that the `server` entry is deprecated and will be removed in a
future version. Clients should use the data from the
[`welcome` message](#welcome-message) instead.


### Protocol version "1.0"

For protocol version `1.0` in the `hello` request, the `params` from the `auth`
field are sent to the Nextcloud backend for [validation](#backend-validation).


### Protocol version "2.0"

For protocol version `2.0` in the `hello` request, the `params` from the `auth`
field must contain a `token` entry containing a [JWT](https://jwt.io/).

The JWT must contain the following fields:
- `iss`: URL of the Nextcloud server that issued the token.
- `iat`: Timestamp when the token has been issued.
- `exp`: Timestamp of the token expiration.
- `sub`: User Id (if known).
- `userdata`: Optional JSON containing more user data.

It must be signed with an RSA, ECDSA or Ed25519 key.

Example token:
```
eyJ0eXAiOiJKV1QiLCJhbGciOiJFUzI1NiJ9.eyJpc3MiOiJodHRwczovL25leHRjbG91ZC1tYXN0ZXIubG9jYWwvIiwiaWF0IjoxNjU0ODQyMDgwLCJleHAiOjE2NTQ4NDIzODAsInN1YiI6ImFkbWluIiwidXNlcmRhdGEiOnsiZGlzcGxheW5hbWUiOiJBZG1pbmlzdHJhdG9yIn19.5rV0jh89_0fG2L-BUPtciu1q49PoYkLboj33EOdD0qQeYcvE7_di2r5WXM1WmKUCOGeX3hzn6qldDMrJBNuxvQ
```

Example public key:
```
-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEIoCsNSCXyxK25zvSKRio0uiBzwub
ONq3tiGTPZo3p2Ogn6wAhhsuSxbFuUQDWMX7Tsu9fDzVdwpRHPT4y3V9cA==
-----END PUBLIC KEY-----
```

Example payload:
```
{
  "iss": "https://nextcloud-master.local/",
  "iat": 1654842080,
  "exp": 1654842380,
  "sub": "admin",
  "userdata": {
    "displayname": "Administrator"
  }
}
```

The public key is retrieved from the capabilities of the Nextcloud instance
in `config` key `hello-v2-token-key` inside `signaling`.

```
        "spreed": {
          "features": [
            "audio",
            "video",
            "chat-v2",
            "conversation-v4",
            ...
          ],
          "config": {
            …
            "signaling": {
              "hello-v2-token-key": "-----BEGIN RSA PUBLIC KEY----- ..."
            }
          }
        },
```


### Backend validation

For `hello` protocol version `1.0`, the server validates the connection request
against the passed auth backend (needs to make sure the passed url / hostname
is in a whitelist).

It performs a POST request and passes the provided `params` as JSON payload in
the body of the request.

Message format (Server -> Auth backend):

    {
      "type": "auth",
      "auth": {
        "version": "the-protocol-version-must-be-1.0",
        "params": {
          ...object containing auth params from hello request...
        }
      }
    }

If the auth params are valid, the backend returns information about the user
that is connecting (as JSON response).

Message format (Auth backend -> Server):

    {
      "type": "auth",
      "auth": {
        "version": "the-protocol-version-must-be-1.0",
        "userid": "the-user-id-for-known-users",
        "user": {
          ...additional data of the user...
        }
      }
    }

Anonymous connections that are not mapped to a user in Nextcloud will have an
empty or omitted `userid` field in the response. If the connection can not be
authorized, the backend returns an error and the hello request will be rejected.


### Error codes

- `unsupported-version`: The requested version is not supported.
- `auth-failed`: The session could not be authenticated.
- `too-many-sessions`: Too many sessions exist for this user id.
- `invalid_backend`: The requested backend URL is not supported.
- `invalid_client_type`: The [client type](#client-types) is not supported.
- `invalid_token`: The passed token is invalid (can happen for
  [client type `internal`](#client-type-internal)).


### Client types

In order to support clients with different functionality on the server, an
optional `type` can be specified in the `auth` struct when connecting to the
server. If no `type` is present, the default value `client` will be used and
a regular "user" client is created internally.

Message format (Client -> Server):

    {
      "id": "unique-request-id",
      "type": "hello",
      "hello": {
        "version": "the-protocol-version",
        "features": ["optional", "list, "of", "client", "feature", "ids"],
        "auth": {
          "type": "the-client-type",
          ...other attributes depending on the client type...
          "params": {
            ...object containing auth params...
          }
        }
      }
    }

The key `params` is required for all client types, other keys depend on the
`type` value.


#### Client type `client` (default)

For the client type `client` (which is the default if no `type` is given), the
URL to the backend server for this client must be given as described above.

This client type must be supported by all server implementations of the
signaling protocol.


#### Client type `internal`

"Internal" clients are used for connections from internal services where the
connection doesn't map to a user (or session) in Nextcloud.

These clients can skip some internal validations, e.g. they can join any room,
even if they have not been invited (which is not possible as the client doesn't
map to a user). This client type is not required to be supported by server
implementations of the signaling protocol, but some additional services might
not work without "internal" clients.

To authenticate the connection, the `params` struct must contain keys `random`
(containing any random string of at least 32 bytes) and `token` containing the
SHA-256 HMAC of `random` with a secret that is shared between the signaling
server and the service connecting to it.


## Resuming sessions

If a connection was interrupted for a client, the server may decide to keep the
session alive for a short time, so the client can reconnect and resume the
session.

In this case, no complete `hello` handshake is required and a client can use
a shorter `hello` request. On success, the session will resume as if no
interruption happened, i.e. the client will stay in his room and will get all
messages from the time the interruption happened.

Message format (Client -> Server):

    {
      "id": "unique-request-id",
      "type": "hello",
      "hello": {
        "version": "the-protocol-version",
        "resumeid": "the-resume-id-from-the-original-hello-response"
      }
    }

Message format (Server -> Client):

    {
      "id": "unique-request-id-from-request",
      "type": "hello",
      "hello": {
        "sessionid": "the-unique-session-id",
        "version": "the-protocol-version"
      }
    }

If the session is no longer valid (e.g. because the resume was too late), the
server will return an error and a normal `hello` handshake has to be performed.


### Error codes

- `no_such_session`: The session id is no longer valid.


## Releasing sessions

By default, the signaling server tries to maintain the session so clients can
resume it in case of intermittent connection problems.

To support cases where a client wants to close the connection and release all
session data, he can send a `bye` message so the server knows he doesn't need
to keep data for resuming.

Message format (Client -> Server):

    {
      "id": "unique-request-id",
      "type": "bye",
      "bye": {}
    }

Message format (Server -> Client):

    {
      "id": "unique-request-id-from-request",
      "type": "bye",
      "bye": {}
    }

After the `bye` has been confirmed, the session can no longer be used.


## Join room

After joining the room through the PHP backend, the room must be changed on the
signaling server, too.

Message format (Client -> Server):

    {
      "id": "unique-request-id",
      "type": "room",
      "room": {
        "roomid": "the-room-id",
        "sessionid": "the-nextcloud-session-id"
      }
    }

- The client can ask about joining a room using this request.
- The session id received from the PHP backend must be passed as `sessionid`.
- The `roomid` can be empty to leave the room the client is currently in
  (local or federated).
- A session can only be connected to one room, i.e. joining a room will leave
  the room currently in.

Message format (Server -> Client):

    {
      "id": "unique-request-id-from-request",
      "type": "room",
      "room": {
        "roomid": "the-room-id",
        "properties": {
          ...additional room properties...
        }
      }
    }

- Sent to confirm a request from the client.
- The `roomid` will be empty if the client is no longer in a room.
- Can be sent without a request if the server moves a client to a room / out of
  the current room or the properties of a room change.


Message format (Server -> Client if already joined before):

    {
      "id": "unique-request-id-from-request",
      "type": "error",
      "error": {
        "code": "already_joined",
        "message": "Human readable error message",
        "details": {
          "roomid": "the-room-id",
          "properties": {
            ...additional room properties...
          }
        }
      }
    }

- Sent if a client tried to join a room it is already in.


### Backend validation

Rooms are managed by the Nextcloud backend, so the signaling server has to
verify that a room exists and a user is allowed to join it.

Message format (Server -> Room backend):

    {
      "type": "room",
      "room": {
        "version": "the-protocol-version-must-be-1.0",
        "roomid": "the-room-id",
        "userid": "the-user-id-for-known-users",
        "sessionid": "the-nextcloud-session-id",
        "action": "join-or-leave"
      }
    }

The `userid` is empty or omitted for anonymous sessions that don't belong to a
user in Nextcloud.

Message format (Room backend -> Server):

    {
      "type": "room",
      "room": {
        "version": "the-protocol-version-must-be-1.0",
        "roomid": "the-room-id",
        "properties": {
          ...additional room properties...
        }
      }
    }

If the room does not exist or can not be joined by the given (or anonymous)
user, the backend returns an error and the room request will be rejected.


### Error codes

- `no_such_room`: The requested room does not exist or the user is not invited
  to the room.


## Join federated room

If the features list contains the id `federation`, the signaling server supports
joining rooms on external signaling servers for Nextcloud instances not
configured in the local server.

Message format (Client -> Server):

    {
      "id": "unique-request-id",
      "type": "room",
      "room": {
        "roomid": "the-local-room-id",
        "sessionid": "the-nextcloud-session-id",
        "federation": {
          "signaling": "wss://remote.domain.invalid/path/to/signaling/",
          "url": "https://remote.domain.invalid/path/to/nextcloud/",
          "roomid": "the-remote-room-id",
          "token": "hello-v2-auth-token-for-remote-signaling-server"
        }
      }
    }

- The remote room id is optional. If omitted, the local room id will be used.
- If a session joins a federated room, any local room will be left.

Message format (Server -> Client):

    {
      "id": "unique-request-id-from-request",
      "type": "room",
      "room": {
        "roomid": "the-local-room-id",
        "properties": {
          ...additional room properties...
        }
      }
    }

- Sent to confirm a request from the client.


### Error codes

- `federation_unsupported`: Federation is not supported by the target server.
- `federation_error`: Error while creating connection to target server
  (additional information might be available in `details`).

Also the error codes from joining a regular room could be returned.


### Events

The signaling server tries to resume the internal proxy session if the
connection to the remote server gets interrupted. To notify clients about these
interruptions, two additional events may be sent from the server to the client:

Connection was interrupted (Server -> Client):

    {
      "type": "event",
      "event": {
        "target": "room",
        "type": "federation_interrupted"
      }
    }


Connection was resumed (Server -> Client):

    {
      "type": "event",
      "event": {
        "target": "room",
        "type": "federation_resumed",
        "resumed": true
      }
    }

The `resumed` flag will be `true` if the existing internal session could be
resumed (i.e. the client stayed in the remote room), or `false` if a new
internal session was created.

If a new internal session was created, the client will receive another `room`
event for the joined room and `join` events for the different participants in
the room.  This should be handled the same as if the direct session could not
be resumed on reconnect.


## Leave room

To leave a room, a [join room](#join-room) message must be sent with an empty
`roomid` parameter.


## Room events

When users join or leave a room, the server generates events that are sent to
all sessions in that room. Such events are also sent to users joining a room
as initial list of users in the room. Multiple user joins/leaves can be batched
into one event to reduce the message overhead.

Message format (Server -> Client, user(s) joined):

    {
      "type": "event"
      "event": {
        "target": "room",
        "type": "join",
        "join": [
          ...list of session objects that joined the room...
        ]
      }
    }

Room event session object:

    {
      "sessionid": "the-unique-session-id",
      "userid": "the-user-id-for-known-users",
      "user": {
        ...additional data of the user as received from the auth backend...
      }
    }

If a session is federated, an additional entry `"federated": true` will be
available.


Message format (Server -> Client, user(s) left):

    {
      "type": "event"
      "event": {
        "target": "room",
        "type": "leave",
        "leave": [
          ...list of session ids that left the room...
        ]
      }
    }

Message format (Server -> Client, user(s) changed):

    {
      "type": "event"
      "event": {
        "target": "room",
        "type": "change",
        "change": [
          ...list of sessions that have changed...
        ]
      }
    }


## Room list events

When users are invited to rooms or are disinvited from them, they get notified
so they can update the list of available rooms.

Message format (Server -> Client, invited to room):

    {
      "type": "event"
      "event": {
        "target": "roomlist",
        "type": "invite",
        "invite": [
          "roomid": "the-room-id",
          "properties": [
            ...additional room properties...
          ]
        ]
      }
    }

Message format (Server -> Client, disinvited from room):

    {
      "type": "event"
      "event": {
        "target": "roomlist",
        "type": "disinvite",
        "disinvite": [
          "roomid": "the-room-id"
        ]
      }
    }


Message format (Server -> Client, room updated):

    {
      "type": "event"
      "event": {
        "target": "roomlist",
        "type": "update",
        "update": [
          "roomid": "the-room-id",
          "properties": [
            ...additional room properties...
          ]
        ]
      }
    }


## Participants list events

When the list of participants or flags of a participant in a room changes, an
event is triggered by the server so clients can update their UI accordingly or
trigger actions like starting calls with other peers.

Message format (Server -> Client, participants change):

    {
      "type": "event"
      "event": {
        "target": "participants",
        "type": "update",
        "update": [
          "roomid": "the-room-id",
          "users": [
            ...list of changed participant objects...
          ]
        ]
      }
    }

If a participant has the `inCall` flag set, he has joined the call of the room
and a WebRTC peerconnection should be established if the local client is also
in the call. In that case the participant information will contain properties
for both the signaling session id (`sessionId`) and the Nextcloud session id
(`nextcloudSessionId`).


### All participants "incall" changed events

When the `inCall` flag of all participants is changed from the backend (see
[backend request](#in-call-state-of-all-participants-changed) below),
a dedicated event is sent that doesn't include information on all participants,
but an `all` flag.

Message format (Server -> Client, incall change):

    {
      "type": "event"
      "event": {
        "target": "participants",
        "type": "update",
        "update": [
          "roomid": "the-room-id",
          "incall": new-incall-state,
          "all": true
        ]
      }
    }


## Room messages

The server can notify clients about events that happened in a room. Currently
such messages are only sent out when chat messages are posted to notify clients
they should load the new messages.

Message format (Server -> Client, chat messages available):

    {
      "type": "event"
      "event": {
        "target": "room",
        "type": "message",
        "message": {
          "roomid": "the-room-id",
          "data": {
            "type": "chat",
            "chat": {
              "refresh": true
            }
          }
        }
      }
    }


## Sending messages between clients

Messages between clients are sent realtime and not stored by the server, i.e.
they are only delivered if the recipient is currently connected. This also
applies to rooms, where only sessions currently in the room will receive the
messages, but not if they join at a later time.

Use this for establishing WebRTC connections between peers, i.e. sending offers,
answers and candidates.

Message format (Client -> Server, to other sessions):

    {
      "id": "unique-request-id",
      "type": "message",
      "message": {
        "recipient": {
          "type": "session",
          "sessionid": "the-session-id-to-send-to"
        },
        "data": {
          ...object containing the data to send...
        }
      }
    }

Message format (Client -> Server, to all sessions of a user):

    {
      "id": "unique-request-id",
      "type": "message",
      "message": {
        "recipient": {
          "type": "user",
          "userid": "the-user-id-to-send-to"
        },
        "data": {
          ...object containing the data to send...
        }
      }
    }

Message format (Client -> Server, to all sessions in the same room):

    {
      "id": "unique-request-id",
      "type": "message",
      "message": {
        "recipient": {
          "type": "room"
        },
        "data": {
          ...object containing the data to send...
        }
      }
    }

Message format (Server -> Client, receive message)

    {
      "type": "message",
      "message": {
        "sender": {
          "type": "the-type-when-sending",
          "sessionid": "the-session-id-of-the-sender",
          "userid": "the-user-id-of-the-sender"
        },
        "data": {
          ...object containing the data of the message...
        }
      }
    }

- The `userid` is omitted if a message was sent by an anonymous user.


## Control messages

Similar to regular messages between clients which can be sent by any session,
messages with type `control` can only be sent if the permission flag `control`
is available.

These messages can be used to perform actions on clients that should only be
possible by some users (e.g. moderators).

Message format (Client -> Server, mute phone):

    {
      "id": "unique-request-id",
      "type": "control",
      "control": {
        "recipient": {
          "type": "session",
          "sessionid": "the-session-id-to-send-to"
        },
        "data": {
          "type": "mute",
          "audio": "audio-flags"
        }
      }
    }

The bit-field `audio-flags` supports the following bits:
- `1`: mute speaking (i.e. phone can no longer talk)
- `2`: mute listening (i.e. phone is on hold and can no longer hear)

To unmute, a value of `0` must be sent.

Message format (Client -> Server, hangup phone):

    {
      "id": "unique-request-id",
      "type": "control",
      "control": {
        "recipient": {
          "type": "session",
          "sessionid": "the-session-id-to-send-to"
        },
        "data": {
          "type": "hangup"
        }
      }
    }

Message format (Client -> Server, send DTMF):

    {
      "id": "unique-request-id",
      "type": "control",
      "control": {
        "recipient": {
          "type": "session",
          "sessionid": "the-session-id-to-send-to"
        },
        "data": {
          "type": "dtmf",
          "digit": "the-digit"
        }
      }
    }

Supported digits are `0`-`9`, `*` and `#`.


## Media publishing


### Start publishing

To start publishing through an SFU, a client must send its offer SDP to the own
session id.

Message format (Client -> Server, send offer):

    {
      "id": "unique-request-id",
      "type": "message",
      "message": {
        "recipient": {
          "type": "session",
          "sessionid": "the-own-session-id"
        },
        "data": {
          "to": "the-own-session-id"
          "type": "offer",
          "sid": "random-client-sid",
          "roomType": "video-or-screen",
          "payload": {
            "nick": "The displayname",
            "type": "offer",
            "sdp": "the-offer-sdp"
          },
          "bitrate": 12345678,
          "audiocodec": "opus",
          "videocodec": "vp9,vp8,h264",
          "vp9profile": "2",
          "h264profile": "42e01f"
        }
      }
    }

The following fields are optional:
- `bitrate`: Limit in bits per second.
- `audiocodec`: One of `opus`, `g722`, `pcmu`, `pcma`, `isac32` and `isac16` or
a comma separated list in order of preference.
- `videocodec`: One of `vp8`, `vp9`, `h264`, `av1` and `h265` or a comma
separated list in order of preference.
- `vp9profile`: VP9-specific profile to prefer, e.g. `2` for `profile-id=2`.
- `h264profile`: H.264-specific profile to prefer, e.g. `42e01f` for
`profile-level-id=42e01f`.


Message format (Server -> Client, send answer):

    {
      "id": "unique-request-id",
      "type": "message",
      "message": {
        "sender": {
          "type": "session",
          "sessionid": "the-own-session-id"
        },
        "data": {
          "from": "the-own-session-id"
          "type": "answer",
          "sid": "random-client-sid-from-offer",
          "roomType": "video-or-screen",
          "payload": {
            "type": "answer",
            "sdp": "the-answer-sdp"
          }
        }
      }
    }


### Exchange candidates

Message format (Client -> Server, send candidate):

    {
      "type": "message",
      "message": {
        "recipient": {
          "type": "session",
          "sessionid": "the-own-session-id"
        },
        "data": {
          "to": "the-own-session-id"
          "type": "candidate",
          "sid": "random-client-sid-from-offer",
          "roomType": "video-or-screen",
          "payload": {
            "candidate": {
              "candiate": "the-candidate-string",
              "sdpMLineIndex": 0,
              "sdpMid": "0"
            }
          }
        }
      }
    }


Message format (Server -> Client, send candidate):

    {
      "type": "message",
      "message": {
        "sender": {
          "type": "session",
          "sessionid": "the-own-session-id"
        },
        "data": {
          "from": "the-own-session-id"
          "to": "the-own-session-id"
          "type": "candidate",
          "sid": "random-client-sid-from-offer",
          "roomType": "video-or-screen",
          "payload": {
            "candidate": {
              "candiate": "the-candidate-string",
              "sdpMLineIndex": 0,
              "sdpMid": "0"
            }
          }
        }
      }
    }


### Request offer from publisher

In order to receive media from existing publishers, a client must request an
offer from them.

Message format (Client -> Server, request offer):

    {
      "type": "message",
      "message": {
        "recipient": {
          "type": "session",
          "sessionid": "the-publisher-session-id"
        },
        "data": {
          "type": "requestoffer",
          "roomType": "video-or-screen"
        }
      }
    }


Message format (Server -> Client, send offer):

    {
      "type": "message",
      "message": {
        "sender": {
          "type": "session",
          "sessionid": "the-publisher-session-id"
        },
        "data": {
          "from": "the-publisher-session-id",
          "to": "the-own-session-id",
          "type": "offer",
          "roomType": "video-or-screen"
          "sid": "random-publisher-sid",
          "roomType": "video-or-screen",
          "payload": {
            "type": "offer",
            "sdp": "the-offer-sdp"
          }
        }
      }
    }


Message format (Client -> Server, send answer):

    {
      "type": "message",
      "message": {
        "recipient": {
          "type": "session",
          "sessionid": "the-publisher-session-id"
        },
        "data": {
          "to": "the-publisher-session-id",
          "type": "answer",
          "roomType": "video-or-screen"
          "sid": "random-publisher-sid",
          "roomType": "video-or-screen",
          "payload": {
            "nick": "The displayname",
            "type": "answer",
            "sdp": "the-answer-sdp"
          }
        }
      }
    }


Candidates are exchanged afterwards as described above.


### Send offer to subscriber

For screensharing streams, the recipients don't know when to request the offer
from the publisher, so in this case, the publisher must trigger receiving the
stream for them, by sending an offer to each subscriber.

Message format (Client -> Server, sendoffer):

    {
      "type": "message",
      "message": {
        "recipient": {
          "type": "session",
          "sessionid": "the-subscriber-session-id"
        },
        "data": {
          "type": "sendoffer",
          "roomType": "video-or-screen"
        }
      }
    }

Afterwards, the server will send an `offer` to the recipient, which has to send
back the `answer` and then candidates will be exchanged.


## Transient data

Transient data can be used to share data in a room that is valid while sessions
are still connected to the room. This can be used for example to have a shared
state in a meeting without having each client to request data from the Nextcloud
server. The data is automatically cleared when the last session disconnects.

Sessions must be in a room and need the permission flag `transient-data` in
order to set or remove values. All sessions in a room automatically receive
all transient data update events.

Transient data is supported if the server returns the `transient-data` feature
id in the [hello response](#establish-connection).


### Set value

Message format (Client -> Server):

    {
      "type": "transient",
      "transient": {
        "type": "set",
        "key": "sample-key",
        "value": "any-json-object",
        "ttl": "optional-ttl"
      }
    }

- The `key` must be a string.
- The `value` can be of any type (i.e. string, number, array, object, etc.).
- The `ttl` is the time to live in nanoseconds. The value will be removed after
  that time (if it is still present).
- Requests to set a value that is already present for the key are silently
  ignored. Any TTL value will be updated / removed.


Message format (Server -> Client):

    {
      "type": "transient",
      "transient": {
        "type": "set",
        "key": "sample-key",
        "value": "any-json-object",
        "oldvalue": "the-previous-value-if-any"
      }
    }

- The `oldvalue` is only present if a previous value was stored for the key.


### Remove value

Message format (Client -> Server):

    {
      "type": "transient",
      "transient": {
        "type": "remove",
        "key": "sample-key"
      }
    }

- The `key` must be a string.
- Requests to remove a key that doesn't exist are silently ignored.


Message format (Server -> Client):

    {
      "type": "transient",
      "transient": {
        "type": "remove",
        "key": "sample-key",
        "oldvalue": "the-previous-value-if-any"
      }
    }

- The `oldvalue` is only present if a previous value was stored for the key.


### Initial data

When sessions initially join a room, they receive the current state of the
transient data.

Message format (Server -> Client):

    {
      "type": "transient",
      "transient": {
        "type": "initial",
        "data": {
          "sample-key": "sample-value",
          ...
        }
      }
    }


## Internal clients

Internal clients can be used by third-party applications to perform tasks that
a regular client can not be used. Examples are adding virtual sessions or
sending media without a regular client connected. This is used for example by
the SIP bridge to publish mixed phone audio and show "virtual" sessions for the
individial phone calls.

See above for details on how to connect as internal client. By default, internal
clients have their "inCall" and the "publishing audio" flags set. Virtual
sessions have their "inCall" and the "publishing phone" flags set.

This can be changed by including the client feature flag `internal-incall`
which will require the client to set the flags as necessary.


### Add virtual session

Message format (Client -> Server):

    {
      "type": "internal",
      "internal": {
        "type": "addsession",
        "addsession": {
          "sessionid": "the-virtual-sessionid",
          "roomid": "the-room-id-to-add-the-session",
          "userid": "optional-user-id",
          "user": {
            ...additional data of the user...
          },
          "flags": "optional-initial-flags",
          "incall": "optional-initial-incall",
          "options": {
            "actorId": "optional-actor-id",
            "actorType": "optional-actor-type",
          }
        }
      }
    }


Phone sessions will have `type` set to `phone` in the additional user data
(which will be included in the `joined` [room event](#room-events)),
`callid` will be the id of the phone call and `number` the target of the call.
The call id will match the one returned for accepted outgoing calls and the
associated session id can be used to hangup a call or send DTMF tones to it.


### Update virtual session

Message format (Client -> Server):

    {
      "type": "internal",
      "internal": {
        "type": "updatesession",
        "updatesession": {
          "sessionid": "the-virtual-sessionid",
          "roomid": "the-room-id-to-update-the-session",
          "flags": "optional-updated-flags",
          "incall": "optional-updated-incall"
        }
      }
    }


### Remove virtual session

Message format (Client -> Server):

    {
      "type": "internal",
      "internal": {
        "type": "removesession",
        "removesession": {
          "sessionid": "the-virtual-sessionid",
          "roomid": "the-room-id-to-add-the-session",
          "userid": "optional-user-id"
        }
      }
    }


### Change inCall flags of internal client

Message format (Client -> Server):

    {
      "type": "internal",
      "internal": {
        "type": "incall",
        "incall": {
          "incall": "the-incall-flags"
        }
      }
    }


# Internal signaling server API

The signaling server provides an internal API that can be called from Nextcloud
to trigger events from the server side.


## Rooms API

The base URL for the rooms API is `/api/vi/room/<roomid>`, all requests must be
sent as `POST` request with proper checksum headers as described above.


### New users invited to room

This can be used to notify users that they are now invited to a room.

Message format (Backend -> Server)

    {
      "type": "invite"
      "invite" {
        "userids": [
          ...list of user ids that are now invited to the room...
        ],
        "alluserids": [
          ...list of all user ids that invited to the room...
        ],
        "properties": [
          ...additional room properties...
        ]
      }
    }


### Users no longer invited to room

This can be used to notify users that they are no longer invited to a room.

Message format (Backend -> Server)

    {
      "type": "disinvite"
      "disinvite" {
        "userids": [
          ...list of user ids that are no longer invited to the room...
        ],
        "alluserids": [
          ...list of all user ids that still invited to the room...
        ]
      }
    }


### Room updated

This can be used to notify about changes to a room. The room properties are the
same as described in section "Join room" above.

Message format (Backend -> Server)

    {
      "type": "update"
      "update" {
        "userids": [
          ...list of user ids that are invited to the room...
        ],
        "properties": [
          ...additional room properties...
        ]
      }
    }


### Room deleted

This can be used to notify about a deleted room. All sessions currently
connected to the room will leave the room.

Message format (Backend -> Server)

    {
      "type": "delete"
      "delete" {
        "userids": [
          ...list of user ids that were invited to the room...
        ]
      }
    }


### Participants changed

This can be used to notify about changed participants.

Message format (Backend -> Server)

    {
      "type": "participants"
      "participants" {
        "changed": [
          ...list of users that were changed...
        ],
        "users": [
          ...list of users in the room...
        ]
      }
    }


### In call state of participants changed

This can be used to notify about participants that changed their `inCall` flag.

Message format (Backend -> Server)

    {
      "type": "incall"
      "incall" {
        "incall": new-incall-state,
        "changed": [
          ...list of users that were changed...
        ],
        "users": [
          ...list of users in the room...
        ]
      }
    }


### In call state of all participants changed

This can be used to notify when all participants changed their `inCall` flag
to the same new value (available if the server returns the `incall-all` feature
id in the [hello response](#establish-connection)).

Message format (Backend -> Server)

    {
      "type": "incall"
      "incall" {
        "incall": new-incall-state,
        "all": true
      }
    }


### Send an arbitrary room message

This can be used to send arbitrary messages to participants in a room. It is
currently used to notify about new chat messages.

Message format (Backend -> Server)

    {
      "type": "message"
      "message" {
        "data": {
          ...arbitrary object to sent to clients...
        }
      }
    }


### Notify sessions to switch to a different room

This can be used to let sessions in a room know that they switch to a different
room (available if the server returns the `switchto` feature). The session ids
sent should be the Talk room session ids.

Message format (Backend -> Server, no additional details)

    {
      "type": "switchto"
      "switchto" {
        "roomid": "target-room-id",
        "sessions": [
          "the-nextcloud-session-id-1",
          "the-nextcloud-session-id-2",
        ]
      }
    }

Message format (Backend -> Server, with additional details)

    {
      "type": "switchto"
      "switchto" {
        "roomid": "target-room-id",
        "sessions": {
          "the-nextcloud-session-id-1": {
            ...arbitrary object to sent to clients...
          },
          "the-nextcloud-session-id-2": null
        }
      }
    }


The signaling server will sent messages to the sessions mentioned in the
received `switchto` event. If a details object was included for a session, it
will be forwarded in the client message, otherwise the `details` will be
omitted.

Message format (Server -> Client):

    {
      "type": "event"
      "event": {
        "target": "room",
        "type": "switchto",
        "switchto": {
          "roomid": "target-room-id",
          "details": {
            ...arbitrary object to sent to clients...
          }
        }
      }
    }

Clients are expected to follow the `switchto` message. If clients don't switch
to the target room after some time, they might get disconnected.


### Start dialout from a room

Use this to start a phone dialout to a new user in a given room.

Message format (Backend -> Server)

    {
      "type": "dialout"
      "dialout" {
        "number": "e164-target-number",
        "options": {
          ...arbitrary options that will be sent back to validate...
        }
      }
    }

Please note that this requires a connected internal client that supports
dialout (e.g. the SIP bridge).

Message format (Server -> Backend, request was accepted)

    {
      "type": "dialout"
      "dialout" {
        "callid": "the-unique-call-id"
      }
    }

Message format (Server -> Backend, request could not be processed)

    {
      "type": "dialout"
      "dialout" {
        "error": {
          "code": "the-internal-message-id",
          "message": "human-readable-error-message",
          "details": {
            ...optional additional details...
          }
        }
      }
    }

A HTTP error status code will be set in this case.
