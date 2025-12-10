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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dlintw/goconf"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/strukturag/nextcloud-spreed-signaling/log"
)

func returnOCS(t *testing.T, w http.ResponseWriter, body []byte) {
	response := OcsResponse{
		Ocs: &OcsBody{
			Meta: OcsMeta{
				Status:     "OK",
				StatusCode: http.StatusOK,
				Message:    "OK",
			},
			Data: body,
		},
	}
	if strings.Contains(t.Name(), "Throttled") {
		response.Ocs.Meta = OcsMeta{
			Status:     "failure",
			StatusCode: 429,
			Message:    "Reached maximum delay",
		}
	}

	data, err := json.Marshal(response)
	require.NoError(t, err)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(data)
	assert.NoError(t, err)
}

func TestPostOnRedirect(t *testing.T) {
	t.Parallel()
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	r := mux.NewRouter()
	r.HandleFunc("/ocs/v2.php/one", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ocs/v2.php/two", http.StatusTemporaryRedirect)
	})
	r.HandleFunc("/ocs/v2.php/two", func(w http.ResponseWriter, r *http.Request) {
		assert := assert.New(t)
		body, err := io.ReadAll(r.Body)
		assert.NoError(err)

		var request map[string]string
		err = json.Unmarshal(body, &request)
		assert.NoError(err)

		returnOCS(t, w, body)
	})

	server := httptest.NewServer(r)
	defer server.Close()

	u, err := url.Parse(server.URL + "/ocs/v2.php/one")
	require.NoError(err)

	config := goconf.NewConfigFile()
	config.AddOption("backend", "allowed", u.Host)
	config.AddOption("backend", "secret", string(testBackendSecret))
	if u.Scheme == "http" {
		config.AddOption("backend", "allowhttp", "true")
	}
	client, err := NewBackendClient(ctx, config, 1, "0.0", nil)
	require.NoError(err)

	request := map[string]string{
		"foo": "bar",
	}
	var response map[string]string
	err = client.PerformJSONRequest(ctx, u, request, &response)
	require.NoError(err)

	if assert.NotNil(t, response) {
		assert.Equal(t, request, response)
	}
}

func TestPostOnRedirectDifferentHost(t *testing.T) {
	t.Parallel()
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	r := mux.NewRouter()
	r.HandleFunc("/ocs/v2.php/one", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://domain.invalid/ocs/v2.php/two", http.StatusTemporaryRedirect)
	})
	server := httptest.NewServer(r)
	defer server.Close()

	u, err := url.Parse(server.URL + "/ocs/v2.php/one")
	require.NoError(err)

	config := goconf.NewConfigFile()
	config.AddOption("backend", "allowed", u.Host)
	config.AddOption("backend", "secret", string(testBackendSecret))
	if u.Scheme == "http" {
		config.AddOption("backend", "allowhttp", "true")
	}
	client, err := NewBackendClient(ctx, config, 1, "0.0", nil)
	require.NoError(err)

	request := map[string]string{
		"foo": "bar",
	}
	var response map[string]string
	err = client.PerformJSONRequest(ctx, u, request, &response)
	if err != nil {
		// The redirect to a different host should have failed.
		require.ErrorIs(err, ErrNotRedirecting)
	} else {
		require.Fail("The redirect should have failed")
	}
}

func TestPostOnRedirectStatusFound(t *testing.T) {
	t.Parallel()
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	r := mux.NewRouter()
	r.HandleFunc("/ocs/v2.php/one", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ocs/v2.php/two", http.StatusFound)
	})
	r.HandleFunc("/ocs/v2.php/two", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if assert.NoError(err) {
			assert.Empty(string(body), "Should not have received any body, got %s", string(body))
		}

		returnOCS(t, w, []byte("{}"))
	})
	server := httptest.NewServer(r)
	defer server.Close()

	u, err := url.Parse(server.URL + "/ocs/v2.php/one")
	require.NoError(err)

	config := goconf.NewConfigFile()
	config.AddOption("backend", "allowed", u.Host)
	config.AddOption("backend", "secret", string(testBackendSecret))
	if u.Scheme == "http" {
		config.AddOption("backend", "allowhttp", "true")
	}
	client, err := NewBackendClient(ctx, config, 1, "0.0", nil)
	require.NoError(err)

	request := map[string]string{
		"foo": "bar",
	}
	var response map[string]string
	err = client.PerformJSONRequest(ctx, u, request, &response)
	if assert.NoError(err) {
		assert.Empty(response, "Expected empty response, got %+v", response)
	}
}

func TestHandleThrottled(t *testing.T) {
	t.Parallel()
	logger := log.NewLoggerForTest(t)
	ctx := log.NewLoggerContext(t.Context(), logger)
	require := require.New(t)
	assert := assert.New(t)
	r := mux.NewRouter()
	r.HandleFunc("/ocs/v2.php/one", func(w http.ResponseWriter, r *http.Request) {
		returnOCS(t, w, []byte("[]"))
	})
	server := httptest.NewServer(r)
	defer server.Close()

	u, err := url.Parse(server.URL + "/ocs/v2.php/one")
	require.NoError(err)

	config := goconf.NewConfigFile()
	config.AddOption("backend", "allowed", u.Host)
	config.AddOption("backend", "secret", string(testBackendSecret))
	if u.Scheme == "http" {
		config.AddOption("backend", "allowhttp", "true")
	}
	client, err := NewBackendClient(ctx, config, 1, "0.0", nil)
	require.NoError(err)

	request := map[string]string{
		"foo": "bar",
	}
	var response map[string]string
	err = client.PerformJSONRequest(ctx, u, request, &response)
	if assert.Error(err) {
		assert.ErrorIs(err, ErrThrottledResponse)
	}
}
