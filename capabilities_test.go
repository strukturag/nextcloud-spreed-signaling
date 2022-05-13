/**
 * Standalone signaling server for the Nextcloud Spreed app.
 * Copyright (C) 2022 struktur AG
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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/mux"
)

func NewCapabilitiesForTest(t *testing.T) (*url.URL, *Capabilities) {
	pool, err := NewHttpClientPool(1, false)
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := NewCapabilities("0.0", pool)
	if err != nil {
		t.Fatal(err)
	}

	r := mux.NewRouter()
	server := httptest.NewServer(r)
	t.Cleanup(func() {
		server.Close()
	})

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	handleCapabilitiesFunc := func(w http.ResponseWriter, r *http.Request) {
		features := []string{
			"foo",
			"bar",
		}
		if strings.Contains(t.Name(), "V3Api") {
			features = append(features, "signaling-v3")
		}
		signaling := map[string]interface{}{
			"foo": "bar",
			"baz": 42,
		}
		config := map[string]interface{}{
			"signaling": signaling,
		}
		response := &CapabilitiesResponse{
			Version: CapabilitiesVersion{
				Major: 20,
			},
			Capabilities: map[string]map[string]interface{}{
				"spreed": {
					"features": features,
					"config":   config,
				},
			},
		}

		data, err := json.Marshal(response)
		if err != nil {
			t.Errorf("Could not marshal %+v: %s", response, err)
		}

		var ocs OcsResponse
		ocs.Ocs = &OcsBody{
			Meta: OcsMeta{
				Status:     "ok",
				StatusCode: http.StatusOK,
				Message:    http.StatusText(http.StatusOK),
			},
			Data: (*json.RawMessage)(&data),
		}
		if data, err = json.Marshal(ocs); err != nil {
			t.Fatal(err)
		}
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(data) // nolint
	}
	r.HandleFunc("/ocs/v2.php/cloud/capabilities", handleCapabilitiesFunc)

	return u, capabilities
}

func TestCapabilities(t *testing.T) {
	url, capabilities := NewCapabilitiesForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	if !capabilities.HasCapabilityFeature(ctx, url, "foo") {
		t.Error("should have capability \"foo\"")
	}
	if capabilities.HasCapabilityFeature(ctx, url, "lala") {
		t.Error("should not have capability \"lala\"")
	}

	expectedString := "bar"
	if value, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); !found {
		t.Error("could not find value for \"foo\"")
	} else if value != expectedString {
		t.Errorf("expected value %s, got %s", expectedString, value)
	}
	if value, found := capabilities.GetStringConfig(ctx, url, "signaling", "baz"); found {
		t.Errorf("should not have found value for \"baz\", got %s", value)
	}
	if value, found := capabilities.GetStringConfig(ctx, url, "signaling", "invalid"); found {
		t.Errorf("should not have found value for \"invalid\", got %s", value)
	}
	if value, found := capabilities.GetStringConfig(ctx, url, "invalid", "foo"); found {
		t.Errorf("should not have found value for \"baz\", got %s", value)
	}

	expectedInt := 42
	if value, found := capabilities.GetIntegerConfig(ctx, url, "signaling", "baz"); !found {
		t.Error("could not find value for \"baz\"")
	} else if value != expectedInt {
		t.Errorf("expected value %d, got %d", expectedInt, value)
	}
	if value, found := capabilities.GetIntegerConfig(ctx, url, "signaling", "foo"); found {
		t.Errorf("should not have found value for \"foo\", got %d", value)
	}
	if value, found := capabilities.GetIntegerConfig(ctx, url, "signaling", "invalid"); found {
		t.Errorf("should not have found value for \"invalid\", got %d", value)
	}
	if value, found := capabilities.GetIntegerConfig(ctx, url, "invalid", "baz"); found {
		t.Errorf("should not have found value for \"baz\", got %d", value)
	}
}
