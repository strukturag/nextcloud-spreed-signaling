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
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"io"
	"sync"
	"unsafe"

	"google.golang.org/protobuf/proto"

	"github.com/strukturag/nextcloud-spreed-signaling/api"
)

const (
	privateSessionName = "private-session"
	publicSessionName  = "public-session"

	// hmacLength specifies the length of the HMAC to use. 80 bits should be enough
	// to prevent tampering.
	hmacLength = 10
)

var (
	sessionHashFunc       = sha256.New
	sessionEncoding       = base64.URLEncoding.WithPadding(base64.NoPadding)
	sessionMarshalOptions = proto.MarshalOptions{
		UseCachedSize: true,
	}
	sessionUnmarshalOptions = proto.UnmarshalOptions{}
	sessionSeparator        = []byte{'|'}
)

type bytesPool struct {
	pool sync.Pool
}

func (p *bytesPool) Get(size int) []byte {
	bb := p.pool.Get()
	if bb == nil {
		return make([]byte, size)
	}

	b := *(bb.(*[]byte))
	if cap(b) < size {
		b = make([]byte, size)
	} else {
		b = b[:size]
	}
	return b
}

func (p *bytesPool) Put(b []byte) {
	p.pool.Put(&b)
}

// SessionIdCodec encodes and decodes session ids.
//
// Inspired by https://github.com/gorilla/securecookie
type SessionIdCodec struct {
	hashKey []byte
	cipher  cipher.Block

	bytesPool bytesPool
	hmacPool  sync.Pool
	dataPool  sync.Pool
}

func NewSessionIdCodec(hashKey []byte, blockKey []byte) (*SessionIdCodec, error) {
	if len(hashKey) == 0 {
		return nil, errors.New("hash key is not set")
	}

	codec := &SessionIdCodec{
		hashKey: hashKey,
		hmacPool: sync.Pool{
			New: func() any {
				return hmac.New(sessionHashFunc, hashKey)
			},
		},
		dataPool: sync.Pool{
			New: func() any {
				return &SessionIdData{}
			},
		},
	}
	if len(blockKey) > 0 {
		block, err := aes.NewCipher(blockKey)
		if err != nil {
			return nil, fmt.Errorf("error creating cipher: %w", err)
		}
		codec.cipher = block
	}
	return codec, nil
}

func (c *SessionIdCodec) encrypt(data []byte) ([]byte, error) {
	iv := c.bytesPool.Get(c.cipher.BlockSize() + len(data))[:c.cipher.BlockSize()]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, fmt.Errorf("error creating iv: %w", err)
	}

	ctr := cipher.NewCTR(c.cipher, iv)
	ctr.XORKeyStream(data, data)
	return append(iv, data...), nil
}

func (c *SessionIdCodec) decrypt(data []byte) ([]byte, error) {
	bs := c.cipher.BlockSize()
	if len(data) <= bs {
		return nil, errors.New("no iv found in data")
	}

	iv := data[:bs]
	data = data[bs:]
	ctr := cipher.NewCTR(c.cipher, iv)
	ctr.XORKeyStream(data, data)
	return data, nil
}

func (c *SessionIdCodec) encodeToString(b []byte) string {
	s := c.bytesPool.Get(sessionEncoding.EncodedLen(len(b)))
	defer c.bytesPool.Put(s)

	sessionEncoding.Encode(s, b)
	return string(s)
}

func (c *SessionIdCodec) decodeFromString(s string) ([]byte, error) {
	b := c.bytesPool.Get(sessionEncoding.DecodedLen(len(s)))
	n, err := sessionEncoding.Decode(b, []byte(s))
	if err != nil {
		c.bytesPool.Put(b)
		return nil, err
	}

	return b[:n], nil
}

func (c *SessionIdCodec) encodeRaw(name string, data *SessionIdData) ([]byte, error) {
	body := c.bytesPool.Get(sessionMarshalOptions.Size(data))
	defer c.bytesPool.Put(body)

	body, err := sessionMarshalOptions.MarshalAppend(body[:0], data)
	if err != nil {
		return nil, fmt.Errorf("error marshaling data: %w", err)
	}

	if c.cipher != nil {
		body, err = c.encrypt(body)
		if err != nil {
			return nil, fmt.Errorf("error encrypting data: %w", err)
		}

		defer c.bytesPool.Put(body)
	}

	h := c.hmacPool.Get().(hash.Hash)
	defer c.hmacPool.Put(h)
	h.Reset()
	h.Write(unsafe.Slice(unsafe.StringData(name), len(name))) // nolint
	h.Write(sessionSeparator)                                 // nolint
	h.Write(body)                                             // nolint
	mac := c.bytesPool.Get(h.Size())
	defer c.bytesPool.Put(mac)
	mac = h.Sum(mac[:0])

	result := c.bytesPool.Get(len(body) + hmacLength)[:0]
	result = append(result, body...)
	result = append(result, mac[:hmacLength]...)
	return result, nil
}

func (c *SessionIdCodec) decodeRaw(name string, value []byte) (*SessionIdData, error) {
	h := c.hmacPool.Get().(hash.Hash)
	defer c.hmacPool.Put(h)
	size := min(hmacLength, h.Size())
	if len(value) <= size {
		return nil, errors.New("no hmac found in session id")
	}

	h.Reset()
	mac := value[len(value)-size:]
	decoded := value[:len(value)-size]

	h.Write(unsafe.Slice(unsafe.StringData(name), len(name))) // nolint
	h.Write(sessionSeparator)                                 // nolint
	h.Write(decoded)                                          // nolint
	check := c.bytesPool.Get(h.Size())
	defer c.bytesPool.Put(check)
	if subtle.ConstantTimeCompare(mac, h.Sum(check[:0])[:hmacLength]) == 0 {
		return nil, errors.New("invalid hmac in session id")
	}

	if c.cipher != nil {
		var err error
		if decoded, err = c.decrypt(decoded); err != nil {
			return nil, fmt.Errorf("invalid session id: %w", err)
		}
	}

	data := c.dataPool.Get().(*SessionIdData)
	if err := sessionUnmarshalOptions.Unmarshal(decoded, data); err != nil {
		c.dataPool.Put(data)
		return nil, fmt.Errorf("invalid session id: %w", err)
	}

	return data, nil
}

func (c *SessionIdCodec) EncodePrivate(sessionData *SessionIdData) (api.PrivateSessionId, error) {
	id, err := c.encodeRaw(privateSessionName, sessionData)
	if err != nil {
		return "", err
	}

	defer c.bytesPool.Put(id)
	return api.PrivateSessionId(c.encodeToString(id)), nil
}

func (c *SessionIdCodec) reverseSessionId(data []byte) {
	for i, j := 0, len(data)-1; i < j; i, j = i+1, j-1 {
		data[i], data[j] = data[j], data[i]
	}
}

func (c *SessionIdCodec) EncodePublic(sessionData *SessionIdData) (api.PublicSessionId, error) {
	encoded, err := c.encodeRaw(publicSessionName, sessionData)
	if err != nil {
		return "", err
	}

	// We are reversing the public session ids because clients compare them
	// to decide who calls whom. The prefix of the session id is increasing
	// (a timestamp) but the suffix the (random) hash.
	// By reversing we move the hash to the front, making the comparison of
	// session ids "random".
	c.reverseSessionId(encoded)

	defer c.bytesPool.Put(encoded)
	return api.PublicSessionId(c.encodeToString(encoded)), nil
}

func (c *SessionIdCodec) DecodePrivate(encodedData api.PrivateSessionId) (*SessionIdData, error) {
	decoded, err := c.decodeFromString(string(encodedData))
	if err != nil {
		return nil, fmt.Errorf("invalid session id: %w", err)
	}
	defer c.bytesPool.Put(decoded)

	return c.decodeRaw(privateSessionName, decoded)
}

func (c *SessionIdCodec) DecodePublic(encodedData api.PublicSessionId) (*SessionIdData, error) {
	decoded, err := c.decodeFromString(string(encodedData))
	if err != nil {
		return nil, fmt.Errorf("invalid session id: %w", err)
	}
	defer c.bytesPool.Put(decoded)

	c.reverseSessionId(decoded)
	return c.decodeRaw(publicSessionName, decoded)
}

func (c *SessionIdCodec) Put(data *SessionIdData) {
	if data != nil {
		data.Reset()
		c.dataPool.Put(data)
	}
}
