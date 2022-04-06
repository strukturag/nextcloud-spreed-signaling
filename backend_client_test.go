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
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/dlintw/goconf"
	"github.com/gorilla/mux"
)

func returnOCS(t *testing.T, w http.ResponseWriter, body []byte) {
	response := OcsResponse{
		Ocs: &OcsBody{
			Meta: OcsMeta{
				Status:     "OK",
				StatusCode: http.StatusOK,
				Message:    "OK",
			},
			Data: (*json.RawMessage)(&body),
		},
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		t.Error(err)
	}
}

func TestPostOnRedirect(t *testing.T) {
	r := mux.NewRouter()
	r.HandleFunc("/ocs/v2.php/one", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ocs/v2.php/two", http.StatusTemporaryRedirect)
	})
	r.HandleFunc("/ocs/v2.php/two", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
			return
		}

		var request map[string]string
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
			return
		}

		returnOCS(t, w, body)
	})

	server := httptest.NewServer(r)
	defer server.Close()

	u, err := url.Parse(server.URL + "/ocs/v2.php/one")
	if err != nil {
		t.Fatal(err)
	}

	config := goconf.NewConfigFile()
	config.AddOption("backend", "allowed", u.Host)
	config.AddOption("backend", "secret", string(testBackendSecret))
	if u.Scheme == "http" {
		config.AddOption("backend", "allowhttp", "true")
	}
	client, err := NewBackendClient(config, 1, "0.0")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	request := map[string]string{
		"foo": "bar",
	}
	var response map[string]string
	err = client.PerformJSONRequest(ctx, u, request, &response)
	if err != nil {
		t.Fatal(err)
	}

	if response == nil || !reflect.DeepEqual(request, response) {
		t.Errorf("Expected %+v, got %+v", request, response)
	}
}

func TestPostOnRedirectDifferentHost(t *testing.T) {
	r := mux.NewRouter()
	r.HandleFunc("/ocs/v2.php/one", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://domain.invalid/ocs/v2.php/two", http.StatusTemporaryRedirect)
	})
	server := httptest.NewServer(r)
	defer server.Close()

	u, err := url.Parse(server.URL + "/ocs/v2.php/one")
	if err != nil {
		t.Fatal(err)
	}

	config := goconf.NewConfigFile()
	config.AddOption("backend", "allowed", u.Host)
	config.AddOption("backend", "secret", string(testBackendSecret))
	if u.Scheme == "http" {
		config.AddOption("backend", "allowhttp", "true")
	}
	client, err := NewBackendClient(config, 1, "0.0")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	request := map[string]string{
		"foo": "bar",
	}
	var response map[string]string
	err = client.PerformJSONRequest(ctx, u, request, &response)
	if err != nil {
		// The redirect to a different host should have failed.
		if !errors.Is(err, ErrNotRedirecting) {
			t.Fatal(err)
		}
	} else {
		t.Fatal("The redirect should have failed")
	}
}

func TestPostOnRedirectStatusFound(t *testing.T) {
	r := mux.NewRouter()
	r.HandleFunc("/ocs/v2.php/one", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ocs/v2.php/two", http.StatusFound)
	})
	r.HandleFunc("/ocs/v2.php/two", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
			return
		}

		if len(body) > 0 {
			t.Errorf("Should not have received any body, got %s", string(body))
		}

		returnOCS(t, w, []byte("{}"))
	})
	server := httptest.NewServer(r)
	defer server.Close()

	u, err := url.Parse(server.URL + "/ocs/v2.php/one")
	if err != nil {
		t.Fatal(err)
	}

	config := goconf.NewConfigFile()
	config.AddOption("backend", "allowed", u.Host)
	config.AddOption("backend", "secret", string(testBackendSecret))
	if u.Scheme == "http" {
		config.AddOption("backend", "allowhttp", "true")
	}
	client, err := NewBackendClient(config, 1, "0.0")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	request := map[string]string{
		"foo": "bar",
	}
	var response map[string]string
	err = client.PerformJSONRequest(ctx, u, request, &response)
	if err != nil {
		t.Error(err)
	}

	if len(response) > 0 {
		t.Errorf("Expected empty response, got %+v", response)
	}
}
