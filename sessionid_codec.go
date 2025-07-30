/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2024 struktur AG
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
	"encoding/base64"
	"fmt"

	"github.com/gorilla/securecookie"
	"google.golang.org/protobuf/proto"
)

type protoSerializer struct {
}

func (s *protoSerializer) Serialize(src any) ([]byte, error) {
	msg, ok := src.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("can't serialize type %T", src)
	}
	return proto.Marshal(msg)
}

func (s *protoSerializer) Deserialize(src []byte, dst any) error {
	msg, ok := dst.(proto.Message)
	if !ok {
		return fmt.Errorf("can't deserialize type %T", src)
	}
	return proto.Unmarshal(src, msg)
}

const (
	privateSessionName = "private-session"
	publicSessionName  = "public-session"
)

type SessionIdCodec struct {
	cookie *securecookie.SecureCookie
}

func NewSessionIdCodec(hashKey []byte, blockKey []byte) *SessionIdCodec {
	cookie := securecookie.New(hashKey, blockKey).
		MaxAge(0).
		SetSerializer(&protoSerializer{})
	return &SessionIdCodec{
		cookie: cookie,
	}
}

func (c *SessionIdCodec) EncodePrivate(sessionData *SessionIdData) (string, error) {
	return c.cookie.Encode(privateSessionName, sessionData)
}

func reverseSessionId(s string) (string, error) {
	// Note that we are assuming base64 encoded strings here.
	decoded, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}

	for i, j := 0, len(decoded)-1; i < j; i, j = i+1, j-1 {
		decoded[i], decoded[j] = decoded[j], decoded[i]
	}
	return base64.URLEncoding.EncodeToString(decoded), nil
}

func (c *SessionIdCodec) EncodePublic(sessionData *SessionIdData) (string, error) {
	encoded, err := c.cookie.Encode(publicSessionName, sessionData)
	if err != nil {
		return "", err
	}

	// We are reversing the public session ids because clients compare them
	// to decide who calls whom. The prefix of the session id is increasing
	// (a timestamp) but the suffix the (random) hash.
	// By reversing we move the hash to the front, making the comparison of
	// session ids "random".
	return reverseSessionId(encoded)
}

func (c *SessionIdCodec) DecodePrivate(encodedData string) (*SessionIdData, error) {
	var data SessionIdData
	if err := c.cookie.Decode(privateSessionName, encodedData, &data); err != nil {
		return nil, err
	}

	return &data, nil
}

func (c *SessionIdCodec) DecodePublic(encodedData string) (*SessionIdData, error) {
	encodedData, err := reverseSessionId(encodedData)
	if err != nil {
		return nil, err
	}

	var data SessionIdData
	if err := c.cookie.Decode(publicSessionName, encodedData, &data); err != nil {
		return nil, err
	}

	return &data, nil
}
