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
	"net/http"
	"testing"
)

func TestBackendChecksum(t *testing.T) {
	t.Parallel()
	rnd := newRandomString(32)
	body := []byte{1, 2, 3, 4, 5}
	secret := []byte("shared-secret")

	check1 := CalculateBackendChecksum(rnd, body, secret)
	check2 := CalculateBackendChecksum(rnd, body, secret)
	if check1 != check2 {
		t.Errorf("Expected equal checksums, got %s and %s", check1, check2)
	}

	if !ValidateBackendChecksumValue(check1, rnd, body, secret) {
		t.Errorf("Checksum %s could not be validated", check1)
	}
	if ValidateBackendChecksumValue(check1[1:], rnd, body, secret) {
		t.Errorf("Checksum %s should not be valid", check1[1:])
	}
	if ValidateBackendChecksumValue(check1[:len(check1)-1], rnd, body, secret) {
		t.Errorf("Checksum %s should not be valid", check1[:len(check1)-1])
	}

	request := &http.Request{
		Header: make(http.Header),
	}
	request.Header.Set("Spreed-Signaling-Random", rnd)
	request.Header.Set("Spreed-Signaling-Checksum", check1)
	if !ValidateBackendChecksum(request, body, secret) {
		t.Errorf("Checksum %s could not be validated from request", check1)
	}
}

func TestValidNumbers(t *testing.T) {
	t.Parallel()
	valid := []string{
		"+12",
		"+12345",
	}
	invalid := []string{
		"+1",
		"12345",
		" +12345",
		" +12345 ",
		"+123-45",
	}
	for _, number := range valid {
		if !isValidNumber(number) {
			t.Errorf("number %s should be valid", number)
		}
	}
	for _, number := range invalid {
		if isValidNumber(number) {
			t.Errorf("number %s should not be valid", number)
		}
	}
}
