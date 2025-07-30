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
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func NewCapabilitiesForTestWithCallback(t *testing.T, callback func(*CapabilitiesResponse, http.ResponseWriter) error) (*url.URL, *Capabilities) {
	require := require.New(t)
	pool, err := NewHttpClientPool(1, false)
	require.NoError(err)
	capabilities, err := NewCapabilities("0.0", pool)
	require.NoError(err)

	r := mux.NewRouter()
	server := httptest.NewServer(r)
	t.Cleanup(func() {
		server.Close()
	})

	u, err := url.Parse(server.URL)
	require.NoError(err)

	handleCapabilitiesFunc := func(w http.ResponseWriter, r *http.Request) {
		features := []string{
			"foo",
			"bar",
		}
		if strings.Contains(t.Name(), "V3Api") {
			features = append(features, "signaling-v3")
		}
		signaling := map[string]any{
			"foo": "bar",
			"baz": 42,
		}
		config := map[string]any{
			"signaling": signaling,
		}
		spreedCapa, _ := json.Marshal(map[string]any{
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

		data, err := json.Marshal(response)
		assert.NoError(t, err, "Could not marshal %+v", response)

		var ocs OcsResponse
		ocs.Ocs = &OcsBody{
			Meta: OcsMeta{
				Status:     "ok",
				StatusCode: http.StatusOK,
				Message:    http.StatusText(http.StatusOK),
			},
			Data: data,
		}
		data, err = json.Marshal(ocs)
		require.NoError(err)

		var cc []string
		if !strings.Contains(t.Name(), "NoCache") {
			if strings.Contains(t.Name(), "ShortCache") {
				cc = append(cc, "max-age=1")
			} else {
				cc = append(cc, "max-age=60")
			}
		}
		if strings.Contains(t.Name(), "MustRevalidate") && !strings.Contains(t.Name(), "NoMustRevalidate") {
			cc = append(cc, "must-revalidate")
		}
		if len(cc) > 0 {
			w.Header().Add("Cache-Control", strings.Join(cc, ", "))
		}
		if strings.Contains(t.Name(), "ETag") {
			h := sha256.New()
			h.Write(data) // nolint
			etag := fmt.Sprintf("\"%s\"", base64.StdEncoding.EncodeToString(h.Sum(nil)))
			w.Header().Add("ETag", etag)
			if inm := r.Header.Get("If-None-Match"); inm == etag {
				if callback != nil {
					if err := callback(response, w); err != nil {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
				}

				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		w.Header().Add("Content-Type", "application/json")
		if callback != nil {
			if err := callback(response, w); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}

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
	assert := assert.New(t)
	url, capabilities := NewCapabilitiesForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	assert.True(capabilities.HasCapabilityFeature(ctx, url, "foo"))
	assert.False(capabilities.HasCapabilityFeature(ctx, url, "lala"))

	expectedString := "bar"
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.True(cached)
	}
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "baz"); assert.False(found, "should not have found value for \"baz\", got %s", value) {
		assert.True(cached)
	}
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "invalid"); assert.False(found, "should not have found value for \"invalid\", got %s", value) {
		assert.True(cached)
	}
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "invalid", "foo"); assert.False(found, "should not have found value for \"baz\", got %s", value) {
		assert.True(cached)
	}

	expectedInt := 42
	if value, cached, found := capabilities.GetIntegerConfig(ctx, url, "signaling", "baz"); assert.True(found) {
		assert.Equal(expectedInt, value)
		assert.True(cached)
	}
	if value, cached, found := capabilities.GetIntegerConfig(ctx, url, "signaling", "foo"); assert.False(found, "should not have found value for \"foo\", got %d", value) {
		assert.True(cached)
	}
	if value, cached, found := capabilities.GetIntegerConfig(ctx, url, "signaling", "invalid"); assert.False(found, "should not have found value for \"invalid\", got %d", value) {
		assert.True(cached)
	}
	if value, cached, found := capabilities.GetIntegerConfig(ctx, url, "invalid", "baz"); assert.False(found, "should not have found value for \"baz\", got %d", value) {
		assert.True(cached)
	}
}

func TestInvalidateCapabilities(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	assert := assert.New(t)
	var called atomic.Uint32
	url, capabilities := NewCapabilitiesForTestWithCallback(t, func(cr *CapabilitiesResponse, w http.ResponseWriter) error {
		called.Add(1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	expectedString := "bar"
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.False(cached)
	}

	value := called.Load()
	assert.EqualValues(1, value)

	// Invalidating will cause the capabilities to be reloaded.
	capabilities.InvalidateCapabilities(url)

	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.False(cached)
	}

	value = called.Load()
	assert.EqualValues(2, value)

	// Invalidating is throttled to about once per minute.
	capabilities.InvalidateCapabilities(url)

	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.True(cached)
	}

	value = called.Load()
	assert.EqualValues(2, value)

	// At a later time, invalidating can be done again.
	SetCapabilitiesGetNow(t, capabilities, func() time.Time {
		return time.Now().Add(2 * time.Minute)
	})

	capabilities.InvalidateCapabilities(url)

	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.False(cached)
	}

	value = called.Load()
	assert.EqualValues(3, value)
}

func TestCapabilitiesNoCache(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	assert := assert.New(t)
	var called atomic.Uint32
	url, capabilities := NewCapabilitiesForTestWithCallback(t, func(cr *CapabilitiesResponse, w http.ResponseWriter) error {
		called.Add(1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	expectedString := "bar"
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.False(cached)
	}

	value := called.Load()
	assert.EqualValues(1, value)

	// Capabilities are cached for some time if no "Cache-Control" header is set.
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.True(cached)
	}

	value = called.Load()
	assert.EqualValues(1, value)

	SetCapabilitiesGetNow(t, capabilities, func() time.Time {
		return time.Now().Add(minCapabilitiesCacheDuration)
	})

	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.False(cached)
	}

	value = called.Load()
	assert.EqualValues(2, value)
}

func TestCapabilitiesShortCache(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	assert := assert.New(t)
	var called atomic.Uint32
	url, capabilities := NewCapabilitiesForTestWithCallback(t, func(cr *CapabilitiesResponse, w http.ResponseWriter) error {
		called.Add(1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	expectedString := "bar"
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.False(cached)
	}

	value := called.Load()
	assert.EqualValues(1, value)

	// Capabilities are cached for some time if no "Cache-Control" header is set.
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.True(cached)
	}

	value = called.Load()
	assert.EqualValues(1, value)

	// The capabilities are cached for a minumum duration.
	SetCapabilitiesGetNow(t, capabilities, func() time.Time {
		return time.Now().Add(minCapabilitiesCacheDuration / 2)
	})

	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.True(cached)
	}

	SetCapabilitiesGetNow(t, capabilities, func() time.Time {
		return time.Now().Add(minCapabilitiesCacheDuration)
	})

	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.False(cached)
	}

	value = called.Load()
	assert.EqualValues(2, value)
}

func TestCapabilitiesNoCacheETag(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	assert := assert.New(t)
	var called atomic.Uint32
	url, capabilities := NewCapabilitiesForTestWithCallback(t, func(cr *CapabilitiesResponse, w http.ResponseWriter) error {
		ct := w.Header().Get("Content-Type")
		switch called.Add(1) {
		case 1:
			assert.NotEmpty(ct, "expected content-type on first request")
		case 2:
			assert.Empty(ct, "expected no content-type on second request")
		}
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	expectedString := "bar"
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.False(cached)
	}

	value := called.Load()
	assert.EqualValues(1, value)

	SetCapabilitiesGetNow(t, capabilities, func() time.Time {
		return time.Now().Add(minCapabilitiesCacheDuration)
	})

	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.True(cached)
	}

	value = called.Load()
	assert.EqualValues(2, value)
}

func TestCapabilitiesCacheNoMustRevalidate(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	assert := assert.New(t)
	var called atomic.Uint32
	url, capabilities := NewCapabilitiesForTestWithCallback(t, func(cr *CapabilitiesResponse, w http.ResponseWriter) error {
		if called.Add(1) == 2 {
			return errors.New("trigger error")
		}

		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	expectedString := "bar"
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.False(cached)
	}

	value := called.Load()
	assert.EqualValues(1, value)

	SetCapabilitiesGetNow(t, capabilities, func() time.Time {
		return time.Now().Add(time.Minute)
	})

	// Expired capabilities can still be used even in case of update errors if
	// "must-revalidate" is not set.
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.True(cached)
	}

	value = called.Load()
	assert.EqualValues(2, value)
}

func TestCapabilitiesNoCacheNoMustRevalidate(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	assert := assert.New(t)
	var called atomic.Uint32
	url, capabilities := NewCapabilitiesForTestWithCallback(t, func(cr *CapabilitiesResponse, w http.ResponseWriter) error {
		if called.Add(1) == 2 {
			return errors.New("trigger error")
		}

		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	expectedString := "bar"
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.False(cached)
	}

	value := called.Load()
	assert.EqualValues(1, value)

	SetCapabilitiesGetNow(t, capabilities, func() time.Time {
		return time.Now().Add(minCapabilitiesCacheDuration)
	})

	// Expired capabilities can still be used even in case of update errors if
	// "must-revalidate" is not set.
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.True(cached)
	}

	value = called.Load()
	assert.EqualValues(2, value)
}

func TestCapabilitiesNoCacheMustRevalidate(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	assert := assert.New(t)
	var called atomic.Uint32
	url, capabilities := NewCapabilitiesForTestWithCallback(t, func(cr *CapabilitiesResponse, w http.ResponseWriter) error {
		if called.Add(1) == 2 {
			return errors.New("trigger error")
		}

		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	expectedString := "bar"
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.False(cached)
	}

	value := called.Load()
	assert.EqualValues(1, value)

	SetCapabilitiesGetNow(t, capabilities, func() time.Time {
		return time.Now().Add(minCapabilitiesCacheDuration)
	})

	// Capabilities will be cleared if "must-revalidate" is set and an error
	// occurs while fetching the updated data.
	capaValue, _, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo")
	assert.False(found, "should not have found value for \"foo\", got %s", capaValue)

	value = called.Load()
	assert.EqualValues(2, value)
}

func TestConcurrentExpired(t *testing.T) {
	t.Parallel()
	CatchLogForTest(t)
	assert := assert.New(t)
	var called atomic.Uint32
	url, capabilities := NewCapabilitiesForTestWithCallback(t, func(cr *CapabilitiesResponse, w http.ResponseWriter) error {
		called.Add(1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	expectedString := "bar"
	if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
		assert.Equal(expectedString, value)
		assert.False(cached)
	}

	count := 100
	start := make(chan struct{})
	var numCached atomic.Uint32
	var numFetched atomic.Uint32
	var finished sync.WaitGroup
	for i := 0; i < count; i++ {
		finished.Add(1)
		go func() {
			defer finished.Done()
			<-start
			if value, cached, found := capabilities.GetStringConfig(ctx, url, "signaling", "foo"); assert.True(found) {
				assert.Equal(expectedString, value)
				if cached {
					numCached.Add(1)
				} else {
					numFetched.Add(1)
				}
			}
		}()
	}

	SetCapabilitiesGetNow(t, capabilities, func() time.Time {
		return time.Now().Add(minCapabilitiesCacheDuration)
	})

	close(start)
	finished.Wait()

	assert.EqualValues(2, called.Load())
	assert.EqualValues(count, numFetched.Load()+numCached.Load())
	assert.EqualValues(1, numFetched.Load())
	assert.EqualValues(count-1, numCached.Load())
}
