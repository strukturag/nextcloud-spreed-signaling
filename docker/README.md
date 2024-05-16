# Docker images for nextcloud-spreed-signaling

## Signaling server

The image for the signaling server can be retrieved from

    strukturag/nextcloud-spreed-signaling:<version>

Replace `version` with the tag or commit you want to use.


### Configuration

The running container can be configured through different environment variables:

- `CONFIG`: Optional name of configuration file to use.
- `HTTP_LISTEN`: Address of HTTP listener.
- `HTTPS_LISTEN`: Address of HTTPS listener.
- `HTTPS_CERTIFICATE`: Name of certificate file for the HTTPS listener.
- `HTTPS_KEY`: Name of private key file for the HTTPS listener.
- `HASH_KEY`: Secret value used to generate checksums of sessions (32 or 64 bytes).
- `BLOCK_KEY`: Key for encrypting data in the sessions (16, 24 or 32 bytes).
- `INTERNAL_SHARED_SECRET_KEY`: Shared secret for connections from internal clients.
- `BACKENDS_ALLOWALL`: Allow all backends. Extremly insecure - use only for development!
- `BACKENDS_ALLOWALL_SECRET`: Secret when `BACKENDS_ALLOWALL` is enabled.
- `BACKENDS`: Space-separated list of backend ids.
- `BACKEND_<ID>_URL`: Url of backend `ID` (where `ID` is the uppercase backend id).
- `BACKEND_<ID>_SHARED_SECRET`: Shared secret for backend `ID` (where `ID` is the uppercase backend id).
- `BACKEND_<ID>_SESSION_LIMIT`: Optional session limit for backend `ID` (where `ID` is the uppercase backend id).
- `BACKEND_<ID>_MAX_STREAM_BITRATE`: Optional maximum bitrate for audio/video streams in backend `ID` (where `ID` is the uppercase backend id).
- `BACKEND_<ID>_MAX_SCREEN_BITRATE`: Optional maximum bitrate for screensharing streams in backend `ID` (where `ID` is the uppercase backend id).
- `NATS_URL`: Optional URL of NATS server.
- `ETCD_ENDPOINTS`: Static list of etcd endpoints (if etcd should be used).
- `ETCD_DISCOVERY_SRV`: Alternative domain to use for DNS SRV configuration of etcd endpoints (if etcd should be used).
- `ETCD_DISCOVERY_SERVICE`: Optional service name for DNS SRV configuration of etcd..
- `ETCD_CLIENT_CERTIFICATE`: Filename of certificate for etcd client.
- `ETCD_CLIENT_KEY`: Filename of private key for etcd client.
- `ETCD_CLIENT_CA`: Filename of CA for etcd client.
- `USE_JANUS`: Set to `1` if Janus should be used as WebRTC backend.
- `JANUS_URL`: Url to Janus server (if `USE_JANUS` is set to `1`).
- `USE_PROXY`: Set to `1` if proxy servers should be used as WebRTC backends.
- `PROXY_TOKEN_ID`: Id of the token to use when connecting to proxy servers.
- `PROXY_TOKEN_KEY`: Private key for the configured token id.
- `PROXY_URLS`: Space-separated list of proxy URLs to connect to.
- `PROXY_DNS_DISCOVERY`: Enable DNS discovery on hostnames of configured static URLs.
- `PROXY_ETCD`: Set to `1` if etcd should be used to configure proxy connections.
- `PROXY_KEY_PREFIX`: Key prefix of proxy entries.
- `MAX_STREAM_BITRATE`: Optional global maximum bitrate for audio/video streams.
- `MAX_SCREEN_BITRATE`: Optional global maximum bitrate for screensharing streams.
- `TURN_API_KEY`: API key that Janus will need to send when requesting TURN credentials.
- `TURN_SECRET`: The shared secret to use for generating TURN credentials.
- `TURN_SERVERS`: A comma-separated list of TURN servers to use.
- `GEOIP_LICENSE`: License key to use when downloading the MaxMind GeoIP database.
- `GEOIP_URL`: Optional URL to download a MaxMind GeoIP database from.
- `GEOIP_OVERRIDES`: Optional space-separated list of overrides for GeoIP lookups.
- `CONTINENT_OVERRIDES`: Optional space-separated list of overrides for continent mappings.
- `STATS_IPS`: Comma-separated list of IP addresses that are allowed to access the stats endpoint.
- `TRUSTED_PROXIES`: Comma-separated list of IPs / networks that are trusted proxies.
- `GRPC_LISTEN`: IP and port to listen on for GRPC requests.
- `GRPC_SERVER_CERTIFICATE`: Certificate to use for the GRPC server.
- `GRPC_SERVER_KEY`: Private key to use for the GRPC server.
- `GRPC_SERVER_CA`: CA certificate that is allowed to issue certificates of GRPC servers.
- `GRPC_CLIENT_CERTIFICATE`: Certificate to use for the GRPC client.
- `GRPC_CLIENT_KEY`: Private key to use for the GRPC client.
- `GRPC_CLIENT_CA`: CA certificate that is allowed to issue certificates of GRPC clients.
- `GRPC_TARGETS`: Comma-separated list of GRPC targets to connect to for clustering mode.
- `GRPC_DNS_DISCOVERY`: Enable DNS discovery on hostnames of configured GRPC targets.
- `GRPC_ETCD`: Set to `1` if etcd should be used to configure GRPC peers.
- `GRPC_TARGET_PREFIX`: Key prefix of GRPC target entries.
- `SKIP_VERIFY`: Set to `true` to skip certificate validation of backends and proxy servers. This should only be enabled during development, e.g. to work with self-signed certificates.

Example with two backends:

    docker run \
      ... \
      -e BACKENDS="foo bar" \
      -e BACKEND_FOO_URL=https://cloud.server1.tld \
      -e BACKEND_FOO_SHARED_SECRET=verysecret \
      -e BACKEND_BAR_URL=https://cloud.server2.tld \
      -e BACKEND_BAR_SHARED_SECRET=moresecret \
      ...

See https://github.com/strukturag/nextcloud-spreed-signaling/blob/master/server.conf.in
for further details on the different options.


## Signaling proxy

The image for the signaling proxy can be retrieved from

    strukturag/nextcloud-spreed-signaling:<version>-proxy

Replace `version` with the tag or commit you want to use.


### Configuration

The running container can be configured through different environment variables:

- `CONFIG`: Optional name of configuration file to use.
- `HTTP_LISTEN`: Address of HTTP listener.
- `COUNTRY`: Optional ISO 3166 country this proxy is located at.
- `JANUS_URL`: Url to Janus server.
- `MAX_STREAM_BITRATE`: Optional maximum bitrate for audio/video streams.
- `MAX_SCREEN_BITRATE`: Optional maximum bitrate for screensharing streams.
- `STATS_IPS`: Comma-separated list of IP addresses that are allowed to access the stats endpoint.
- `TRUSTED_PROXIES`: Comma-separated list of IPs / networks that are trusted proxies.
- `ETCD_ENDPOINTS`: Static list of etcd endpoints (if etcd should be used).
- `ETCD_DISCOVERY_SRV`: Alternative domain to use for DNS SRV configuration of etcd endpoints (if etcd should be used).
- `ETCD_DISCOVERY_SERVICE`: Optional service name for DNS SRV configuration of etcd..
- `ETCD_CLIENT_CERTIFICATE`: Filename of certificate for etcd client.
- `ETCD_CLIENT_KEY`: Filename of private key for etcd client.
- `ETCD_CLIENT_CA`: Filename of CA for etcd client.
- `TOKENS_ETCD`: Set to `1` if etcd should be used to configure tokens.
- `TOKEN_KEY_FORMAT`: Format of key name to retrieve the public key from, "%s" will be replaced with the token id.
- `TOKENS`: Space-separated list of token ids.
- `TOKEN_<ID>_KEY`: Filename of public key for token `ID` (where `ID` is the uppercase token id).

Example with two tokens:

    docker run \
      ... \
      -e TOKENS="foo signaling.server1.tld" \
      -e TOKEN_FOO_KEY=/path/to/foo.key \
      -e TOKEN_SIGNALING_SERVER1_TLD_KEY=/path/to/signaling.server1.tld.key \
      ...

See https://github.com/strukturag/nextcloud-spreed-signaling/blob/master/proxy.conf.in
for further details on the different options.
