# Spreed standalone signaling server

![Build Status](https://github.com/strukturag/nextcloud-spreed-signaling/workflows/test/badge.svg)
[![Coverage Status](https://coveralls.io/repos/github/strukturag/nextcloud-spreed-signaling/badge.svg?branch=master)](https://coveralls.io/github/strukturag/nextcloud-spreed-signaling?branch=master)
[![Documentation Status](https://readthedocs.org/projects/nextcloud-spreed-signaling/badge/?version=latest)](https://nextcloud-spreed-signaling.readthedocs.io/en/latest/?badge=latest)
[![Go Report](https://goreportcard.com/badge/github.com/strukturag/nextcloud-spreed-signaling)](https://goreportcard.com/report/github.com/strukturag/nextcloud-spreed-signaling)

This repository contains the standalone signaling server which can be used for
Nextcloud Talk (https://apps.nextcloud.com/apps/spreed).

See https://nextcloud-talk.readthedocs.io/en/latest/standalone-signaling-api-v1/ for further
information on the API of the signaling server.


## Building

The following tools are required for building the signaling server.

- git
- go >= 1.17
- make

All other dependencies are fetched automatically while building.

    $ make build

or on FreeBSD

    $ gmake build

Afterwards the binary is created as `bin/signaling`.


## Configuration

A default configuration file is included as `server.conf.in`. Copy this to
`server.conf` and adjust as necessary for the local setup. See the file for
comments about the different parameters that can be changed.


## Running

The signaling server connects to a NATS server (https://nats.io/) to distribute
messages between different instances. See the NATS documentation on how to set
up a server and run it.

Once the NATS server is running (and the URL to it is configured for the
signaling server), you can start the signaling server.

    $ ./bin/signaling

By default, the configuration is loaded from `server.conf` in the current
directory, but a different path can be passed through the `--config` option.

    $ ./bin/signaling --config /etc/signaling/server.conf

### Running as daemon

#### systemd

Create a dedicated group and user:

```bash
sudo groupadd --system signaling
sudo useradd --system \
    --gid signaling \
    --shell /usr/sbin/nologin \
    --comment "Standalone signaling server for Nextcloud Talk." \
    signaling
```

Copy `server.conf.in` to `/etc/signaling/server.conf` and fix permissions:

```bash
sudo chmod 600 /etc/signaling/server.conf
sudo chown signaling: /etc/signaling/server.conf
```

Copy `dist/init/systemd/signaling.service` to `/etc/systemd/system/signaling.service` (adjust abs. path in `ExecStart` to match your binary location!)

Enable and start service:

```bash
systemctl enable signaling.service
systemctl start signaling.service
```

### Running with Docker

#### Docker Compose

You will likely have to adjust the Janus command line options depending on the exact network configuration on your server. Refer to [Setup of Janus](#setup-of-janus) and the Janus documentation for how to configure your Janus server.

Copy `server.conf.in` to `server.conf` and adjust it to your liking.

If you're using the [docker-compose.yml](docker-compose.yml) configuration as is, the MCU Url must be set to `ws://localhost:8188`, the NATS Url must be set to `nats://localhost:4222`, and TURN Servers must be set to `turn:localhost:3478?transport=udp,turn:localhost:3478?transport=tcp`.

```bash
docker-compose build
docker-compose up -d
```

## Setup of NATS server

There is a detailed description on how to install and run the NATS server
available at http://nats.io/documentation/tutorials/gnatsd-install/

You can use the `gnatsd.conf` file as base for the configuration of the NATS
server.


## Setup of Janus

A Janus server (from https://github.com/meetecho/janus-gateway) can be used to
act as a WebRTC gateway. See the documentation of Janus on how to configure and
run the server. At least the `VideoRoom` plugin and the websocket transport of
Janus must be enabled.

The signaling server uses the `VideoRoom` plugin of Janus to manage sessions.
All gateway details are hidden from the clients, all messages are sent through
the signaling server. Only WebRTC media is exchanged directly between the
gateway and the clients.

Edit the `server.conf` and enter the URL to the websocket endpoint of Janus in
the section `[mcu]` and key `url`. During startup, the signaling server will
connect to Janus and log information of the gateway.

The maximum bandwidth per publishing stream can also be configured in the
section `[mcu]`, see properties `maxstreambitrate` and `maxscreenbitrate`.


### Use multiple Janus servers

To scale the setup and add high availability, a signaling server can connect to
one or multiple proxy servers that each provide access to a single Janus server.

For that, set the `type` key in section `[mcu]` to `proxy` and set `url` to a
space-separated list of URLs where a proxy server is running.

Each signaling server that connects to a proxy needs a unique token id and a
public / private RSA keypair. The token id must be configured as `token_id` in
section `[mcu]`, the path to the private key file as `token_key`.


### Setup of proxy server

The proxy server is built with the standard make command `make build` as
`bin/proxy` binary. Copy the `proxy.conf.in` as `proxy.conf` and edit section
`[tokens]` to the list of allowed token ids and filenames of the public keys
for each token id. See the comments in `proxy.conf.in` for other configuration
options.

When the proxy process receives a `SIGHUP` signal, the list of allowed token
ids / public keys is reloaded. A `SIGUSR1` signal can be used to shutdown a
proxy process gracefully after all clients have been disconnected. No new
publishers will be accepted in this case.


## Setup of frontend webserver

Usually the standalone signaling server is running behind a webserver that does
the SSL protocol or acts as a load balancer for multiple signaling servers.

The configuration examples below assume a pre-configured webserver (nginx or
Apache) with a working HTTPS setup, that is listening on the external interface
of the server hosting the standalone signaling server.

After everything has been set up, the configuration can be tested using `curl`:

    $ curl -i https://myserver.domain.invalid/standalone-signaling/api/v1/welcome
    HTTP/1.1 200 OK
    Date: Thu, 05 Jul 2018 09:28:08 GMT
    Server: nextcloud-spreed-signaling/1.0.0
    Content-Type: application/json; charset=utf-8
    Content-Length: 59

    {"nextcloud-spreed-signaling":"Welcome","version":"1.0.0"}


### nginx

Nginx can be used as frontend for the standalone signaling server without any
additional requirements.

The backend should be configured separately so it can be changed in a single
location and also to allow using multiple backends from a single frontend
server.

Assuming the standalone signaling server is running on the local interface on
port `8080` below, add the following block to the nginx server definition in
`/etc/nginx/sites-enabled` (just before the `server` definition):

    upstream signaling {
        server 127.0.0.1:8080;
    }

To proxy all requests for the standalone signaling to the correct backend, the
following `location` block must be added inside the `server` definition of
the same file:

    location /standalone-signaling/ {
        proxy_pass http://signaling/;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }

    location /standalone-signaling/spreed {
        proxy_pass http://signaling/spreed;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "Upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }


Example (e.g. `/etc/nginx/sites-enabled/default`):

    upstream signaling {
        server 127.0.0.1:8080;
    }

    server {
        listen 443 ssl http2;
        server_name myserver.domain.invalid;

        # ... other existing configuration ...

        location /standalone-signaling/ {
            proxy_pass http://signaling/;
            proxy_http_version 1.1;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        }

        location /standalone-signaling/spreed {
            proxy_pass http://signaling/spreed;
            proxy_http_version 1.1;
            proxy_set_header Upgrade $http_upgrade;
            proxy_set_header Connection "Upgrade";
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        }
    }


### Apache

To configure the Apache webservice as frontend for the standalone signaling
server, the modules `mod_proxy_http` and `mod_proxy_wstunnel` must be enabled
so WebSocket and API backend requests can be proxied:

    $ sudo a2enmod proxy
    $ sudo a2enmod proxy_http
    $ sudo a2enmod proxy_wstunnel

Now the Apache `VirtualHost` configuration can be extended to forward requests
to the standalone signaling server (assuming the server is running on the local
interface on port `8080` below):

    <VirtualHost *:443>

        # ... existing configuration ...

        # Enable proxying Websocket requests to the standalone signaling server.
        ProxyPass "/standalone-signaling/"  "ws://127.0.0.1:8080/"

        RewriteEngine On
        # Websocket connections from the clients.
        RewriteRule ^/standalone-signaling/spreed/$ - [L]
        # Backend connections from Nextcloud.
        RewriteRule ^/standalone-signaling/api/(.*) http://127.0.0.1:8080/api/$1 [L,P]

        # ... existing configuration ...

    </VirtualHost>

### Caddy

#### v1

[Caddy (v1)](https://caddyserver.com/v1/) configuration:

```
myserver.domain.invalid {
  proxy /standalone-signaling/ http://127.0.0.1:8080 {
    without /standalone-signaling
    transparent
    websocket
  }
}
```

#### v2

[Caddy (v2)](https://caddyserver.com/) configuration:

```
myserver.domain.invalid {
  route /standalone-signaling/* {
    uri strip_prefix /standalone-signaling
    reverse_proxy http://127.0.0.1:8080
  }
}
```

## Setup of Nextcloud Talk

Login to your Nextcloud as admin and open the additional settings page. Scroll
down to the "Talk" section and enter the base URL of your standalone signaling
server in the field "External signaling server".
Please note that you have to use `https` if your Nextcloud is also running on
`https`. Usually you should enter `https://myhostname/standalone-signaling` as
URL.

The value "Shared secret for external signaling server" must be the same as the
property `secret` in section `backend` of your `server.conf`.

If you are using a self-signed certificate for development, you need to uncheck
the box `Validate SSL certificate` so backend requests from Nextcloud to the
signaling server can be performed.


## Benchmarking the server

A simple client exists to benchmark the server. Please note that the features
that are benchmarked might not cover the whole functionality, check the
implementation in `src/client` for details on the client.

To authenticate new client connections to the signaling server, the client
starts a dummy authentication handler on a local interface and passes the URL
in the `hello` request. Therefore the signaling server should be configured to
allow all backend hosts (option `allowall` in section `backend`).

The client is not compiled by default, but can be using the `client` target:

    $ make client

Usage:

    $ ./bin/client
    Usage of ./bin/client:
      -addr string
            http service address (default "localhost:28080")
      -config string
            config file to use (default "server.conf")
      -maxClients int
            number of client connections (default 100)


## Running multiple signaling servers

IMPORTANT: This is considered experimental and might not work with all
functionality of the signaling server, especially when using the Janus
integration.

The signaling server uses the NATS server to send messages to peers that are
not connected locally. Therefore multiple signaling servers running on different
hosts can use the same NATS server to build a simple cluster, allowing more
simultaneous connections and distribute the load.

To set this up, make sure all signaling servers are using the same settings for
their `session` keys and the `secret` in the `backend` section. Also the URL to
the NATS server (option `url` in section `nats`) must point to the same NATS
server.

If all this is setup correctly, clients can connect to either of the signaling
servers and exchange messages between them.
