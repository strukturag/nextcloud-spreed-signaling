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
	"bytes"
	"encoding/json"
	"io"
	"sync"
)

type BufferPool struct {
	buffers     sync.Pool
	copyBuffers sync.Pool
}

func (p *BufferPool) Get() *bytes.Buffer {
	b := p.buffers.Get()
	if b == nil {
		return bytes.NewBuffer(nil)
	}

	return b.(*bytes.Buffer)
}

func (p *BufferPool) Put(b *bytes.Buffer) {
	if b == nil {
		return
	}

	b.Reset()
	p.buffers.Put(b)
}

func (p *BufferPool) ReadAll(r io.Reader) (*bytes.Buffer, error) {
	buf := p.copyBuffers.Get()
	if buf == nil {
		buf = make([]byte, 1024)
	}
	defer p.copyBuffers.Put(buf)

	b := p.Get()
	if _, err := io.CopyBuffer(b, r, buf.([]byte)); err != nil {
		p.Put(b)
		return nil, err
	}

	return b, nil
}

func (p *BufferPool) MarshalAsJSON(v any) (*bytes.Buffer, error) {
	b := p.Get()
	encoder := json.NewEncoder(b)
	if err := encoder.Encode(v); err != nil {
		p.Put(b)
		return nil, err
	}

	return b, nil
}
