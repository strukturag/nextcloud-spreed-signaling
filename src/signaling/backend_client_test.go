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
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/dlintw/goconf"
	"github.com/gorilla/mux"
	"golang.org/x/net/context"
)

func testUrls(t *testing.T, client *BackendClient, valid_urls []string, invalid_urls []string) {
	for _, u := range valid_urls {
		parsed, err := url.ParseRequestURI(u)
		if err != nil {
			t.Errorf("The url %s should be valid, got %s", u, err)
			continue
		}
		if !client.IsUrlAllowed(parsed) {
			t.Errorf("The url %s should be allowed", u)
		}
	}
	for _, u := range invalid_urls {
		parsed, _ := url.ParseRequestURI(u)
		if client.IsUrlAllowed(parsed) {
			t.Errorf("The url %s should not be allowed", u)
		}
	}
}

func TestIsUrlAllowed(t *testing.T) {
	valid_urls := []string{
		"http://domain.invalid",
		"https://domain.invalid",
	}
	invalid_urls := []string{
		"http://otherdomain.invalid",
		"https://otherdomain.invalid",
		"domain.invalid",
	}
	client := &BackendClient{
		whitelistAll: false,
		whitelist: map[string]bool{
			"domain.invalid": true,
		},
	}
	testUrls(t, client, valid_urls, invalid_urls)
}

func TestIsUrlAllowed_EmptyWhitelist(t *testing.T) {
	valid_urls := []string{}
	invalid_urls := []string{
		"http://domain.invalid",
		"https://domain.invalid",
		"domain.invalid",
	}
	client := &BackendClient{
		whitelistAll: false,
	}
	testUrls(t, client, valid_urls, invalid_urls)
}

func TestIsUrlAllowed_WhitelistAll(t *testing.T) {
	valid_urls := []string{
		"http://domain.invalid",
		"https://domain.invalid",
	}
	invalid_urls := []string{
		"domain.invalid",
	}
	client := &BackendClient{
		whitelistAll: true,
	}
	testUrls(t, client, valid_urls, invalid_urls)
}

func TestPostOnRedirect(t *testing.T) {
	r := mux.NewRouter()
	r.HandleFunc("/ocs/v2.php/one", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ocs/v2.php/two", http.StatusFound)
	})
	r.HandleFunc("/ocs/v2.php/two", func(w http.ResponseWriter, r *http.Request) {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
			return
		}

		var request map[string]string
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
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

		w.WriteHeader(http.StatusOK)
		w.Write(data)
	})

	server := httptest.NewServer(r)
	defer server.Close()

	config := &goconf.ConfigFile{}
	client, err := NewBackendClient(config, 1, "0.0")
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	u, err := url.Parse(server.URL + "/ocs/v2.php/one")
	if err != nil {
		t.Fatal(err)
	}
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
