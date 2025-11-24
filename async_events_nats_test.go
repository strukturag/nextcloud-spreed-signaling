/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2025 struktur AG
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
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func Benchmark_GetSubjectForSessionId(b *testing.B) {
	backend := &Backend{
		id: "compat",
	}
	data := &SessionIdData{
		Sid:       1,
		Created:   timestamppb.Now(),
		BackendId: backend.Id(),
	}
	codec := NewSessionIdCodec([]byte("12345678901234567890123456789012"), []byte("09876543210987654321098765432109"))
	sid, err := codec.EncodePublic(data)
	if err != nil {
		b.Fatalf("could not create session id: %s", err)
	}
	for b.Loop() {
		GetSubjectForSessionId(sid, backend)
	}
}
