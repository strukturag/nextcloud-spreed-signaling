#!/bin/bash
#
# Standalone signaling server for the Nextcloud Spreed app.
# Copyright (C) 2022 struktur AG
#
# @author Joachim Bauch <bauch@struktur.de>
#
# @license GNU AGPL version 3 or any later version
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <http://www.gnu.org/licenses/>.
#
set -e

if [ -z "$CONFIG" ]; then
  echo "No configuration filename given in CONFIG environment variable"
  exit 1
fi

if [ ! -f "$CONFIG" ]; then
  echo "Preparing signaling proxy configuration in $CONFIG ..."
  cp /config/proxy.conf.in "$CONFIG"

  if [ ! -z "$HTTP_LISTEN" ]; then
    sed -i "s|#listen = 127.0.0.1:9090|listen = $HTTP_LISTEN|" "$CONFIG"
  fi

  if [ ! -z "$COUNTRY" ]; then
    sed -i "s|#country =.*|country = $COUNTRY|" "$CONFIG"
  fi

  HAS_ETCD=
  if [ ! -z "$ETCD_ENDPOINTS" ]; then
    sed -i "s|#endpoints =.*|endpoints = $ETCD_ENDPOINTS|" "$CONFIG"
    HAS_ETCD=1
  else
    if [ ! -z "$ETCD_DISCOVERY_SRV" ]; then
      sed -i "s|#discoverysrv =.*|discoverysrv = $ETCD_DISCOVERY_SRV|" "$CONFIG"
      HAS_ETCD=1
    fi
    if [ ! -z "$ETCD_DISCOVERY_SERVICE" ]; then
      sed -i "s|#discoveryservice =.*|discoveryservice = $ETCD_DISCOVERY_SERVICE|" "$CONFIG"
    fi
  fi
  if [ ! -z "$HAS_ETCD" ]; then
    if [ ! -z "$ETCD_CLIENT_KEY" ]; then
      sed -i "s|#clientkey = /path/to/etcd-client.key|clientkey = $ETCD_CLIENT_KEY|" "$CONFIG"
    fi
    if [ ! -z "$ETCD_CLIENT_CERTIFICATE" ]; then
      sed -i "s|#clientcert = /path/to/etcd-client.crt|clientcert = $ETCD_CLIENT_CERTIFICATE|" "$CONFIG"
    fi
    if [ ! -z "$ETCD_CLIENT_CA" ]; then
      sed -i "s|#cacert = /path/to/etcd-ca.crt|cacert = $ETCD_CLIENT_CA|" "$CONFIG"
    fi
  fi

  if [ ! -z "$JANUS_URL" ]; then
    sed -i "s|url =.*|url = $JANUS_URL|" "$CONFIG"
  else
    sed -i "s|url =.*|#url =|" "$CONFIG"
  fi
  if [ ! -z "$MAX_STREAM_BITRATE" ]; then
    sed -i "s|#maxstreambitrate =.*|maxstreambitrate = $MAX_STREAM_BITRATE|" "$CONFIG"
  fi
  if [ ! -z "$MAX_SCREEN_BITRATE" ]; then
    sed -i "s|#maxscreenbitrate =.*|maxscreenbitrate = $MAX_SCREEN_BITRATE|" "$CONFIG"
  fi

  if [ ! -z "$TOKENS_ETCD" ]; then
    if [ -z "$HAS_ETCD" ]; then
      echo "No etcd endpoint configured, can't use etcd for proxy tokens"
      exit 1
    fi

    sed -i "s|tokentype =.*|tokentype = etcd|" "$CONFIG"

    if [ ! -z "$TOKEN_KEY_FORMAT" ]; then
      sed -i "s|#keyformat =.*|keyformat = $TOKEN_KEY_FORMAT|" "$CONFIG"
    fi
  else
    sed -i "s|\[tokens\]|#[tokens]|" "$CONFIG"
    echo >> "$CONFIG"
    echo "[tokens]" >> "$CONFIG"
    for token in $TOKENS; do
      declare var="TOKEN_${token^^}_KEY"
      var=$(echo $var | sed "s|\.|_|")
      if [ ! -z "${!var}" ]; then
        echo "$token = ${!var}" >> "$CONFIG"
      fi
    done
    echo >> "$CONFIG"
  fi

  if [ ! -z "$STATS_IPS" ]; then
    sed -i "s|#allowed_ips =.*|allowed_ips = $STATS_IPS|" "$CONFIG"
  fi
fi

echo "Starting signaling proxy with $CONFIG ..."
exec "$@"
