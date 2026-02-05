/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2026 struktur AG
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
package internal

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

type testError struct{}

func (e testError) Error() string {
	return "test error"
}

func TestAsErrorType(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)

	if e, ok := AsErrorType[*testError](nil); assert.False(ok) {
		assert.Nil(e)
	}

	err1 := &testError{}
	if e, ok := AsErrorType[*testError](err1); assert.True(ok) {
		assert.Same(err1, e)
	}

	err2 := errors.New("other error")
	if e, ok := AsErrorType[*testError](err2); assert.False(ok) {
		assert.Nil(e)
	}

	err3 := fmt.Errorf("wrapped error: %w", err1)
	if e, ok := AsErrorType[*testError](err3); assert.True(ok) {
		assert.Same(err1, e)
	}
}
