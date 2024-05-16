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
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
)

func NewCapabilitiesForTestWithCallback(t *testing.T, callback func(*CapabilitiesResponse)) (*url.URL, *Capabilities) {
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
		spreedCapa, _ := json.Marshal(map[string]interface{}{
			"features": features,
			"config":   config,
		})
		emptyArray := []byte("[]")
		response := &CapabilitiesResponse{
			Version: CapabilitiesVersion{
				Major: 20,
			},
			Capabilities: map[string]json.RawMessage{
				"anotherApp": emptyArray,
				"spreed":     spreedCapa,
			},
		}

		if callback != nil {
			callback(response)
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
			Data: data,
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

func NewCapabilitiesForTest(t *testing.T) (*url.URL, *Capabilities) {
	return NewCapabilitiesForTestWithCallback(t, nil)
}

func SetCapabilitiesGetNow(t *testing.T, capabilities *Capabilities, f func() time.Time) {
	capabilities.mu.Lock()
	defer capabilities.mu.Unlock()

	old := capabilities.getNow

	t.Cleanup(func() {
		capabilities.mu.Lock()
		defer capabilities.mu.Unlock()

		capabilities.getNow = old
	})

	capabilities.getNow = f
}

func TestCapabilities(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
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
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); !found {
		t.Error("could not find value for \"foo\"")
	} else if value != expectedString {
		t.Errorf("expected value %s, got %s", expectedString, value)
	} else if !cached {
		t.Errorf("expected cached response")
	}
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "baz"); found {
		t.Errorf("should not have found value for \"baz\", got %s", value)
	} else if !cached {
		t.Errorf("expected cached response")
	}
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "invalid"); found {
		t.Errorf("should not have found value for \"invalid\", got %s", value)
	} else if !cached {
		t.Errorf("expected cached response")
	}
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "invalid", "foo"); found {
		t.Errorf("should not have found value for \"baz\", got %s", value)
	} else if !cached {
		t.Errorf("expected cached response")
	}

	expectedInt := 42
	if value, cached, found := capabilities.GetIntegerConfig(ctx, url, "signaling", "baz"); !found {
		t.Error("could not find value for \"baz\"")
	} else if value != expectedInt {
		t.Errorf("expected value %d, got %d", expectedInt, value)
	} else if !cached {
		t.Errorf("expected cached response")
	}
	if value, cached, found := capabilities.GetIntegerConfig(ctx, url, "signaling", "foo"); found {
		t.Errorf("should not have found value for \"foo\", got %d", value)
	} else if !cached {
		t.Errorf("expected cached response")
	}
	if value, cached, found := capabilities.GetIntegerConfig(ctx, url, "signaling", "invalid"); found {
		t.Errorf("should not have found value for \"invalid\", got %d", value)
	} else if !cached {
		t.Errorf("expected cached response")
	}
	if value, cached, found := capabilities.GetIntegerConfig(ctx, url, "invalid", "baz"); found {
		t.Errorf("should not have found value for \"baz\", got %d", value)
	} else if !cached {
		t.Errorf("expected cached response")
	}
}

func TestInvalidateCapabilities(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	var called atomic.Uint32
	url, capabilities := NewCapabilitiesForTestWithCallback(t, func(cr *CapabilitiesResponse) {
		called.Add(1)
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	expectedString := "bar"
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); !found {
		t.Error("could not find value for \"foo\"")
	} else if value != expectedString {
		t.Errorf("expected value %s, got %s", expectedString, value)
	} else if cached {
		t.Errorf("expected direct response")
	}

	if value := called.Load(); value != 1 {
		t.Errorf("expected called %d, got %d", 1, value)
	}

	// Invalidating will cause the capabilities to be reloaded.
	capabilities.InvalidateCapabilities(url)

	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); !found {
		t.Error("could not find value for \"foo\"")
	} else if value != expectedString {
		t.Errorf("expected value %s, got %s", expectedString, value)
	} else if cached {
		t.Errorf("expected direct response")
	}

	if value := called.Load(); value != 2 {
		t.Errorf("expected called %d, got %d", 2, value)
	}

	// Invalidating is throttled to about once per minute.
	capabilities.InvalidateCapabilities(url)

	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); !found {
		t.Error("could not find value for \"foo\"")
	} else if value != expectedString {
		t.Errorf("expected value %s, got %s", expectedString, value)
	} else if !cached {
		t.Errorf("expected cached response")
	}

	if value := called.Load(); value != 2 {
		t.Errorf("expected called %d, got %d", 2, value)
	}

	// At a later time, invalidating can be done again.
	SetCapabilitiesGetNow(t, capabilities, func() time.Time {
		return time.Now().Add(2 * time.Minute)
	})

	capabilities.InvalidateCapabilities(url)

	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); !found {
		t.Error("could not find value for \"foo\"")
	} else if value != expectedString {
		t.Errorf("expected value %s, got %s", expectedString, value)
	} else if cached {
		t.Errorf("expected direct response")
	}

	if value := called.Load(); value != 3 {
		t.Errorf("expected called %d, got %d", 3, value)
	}
}
