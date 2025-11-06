# Prometheus metrics

The signaling server and -proxy expose various metrics that can be queried by a
[Prometheus](https://prometheus.io/) server from the `/metrics` endpoint.

Only clients connecting from an IP that is included in the `allowed_ips` value
of the `[stats]` entry in the configuration file are allowed to query the
metrics.


## Available metrics

The following metrics are available:

| Metric                                            | Type      | Since     | Description                                                               | Labels                            |
| :------------------------------------------------ | :-------- | --------: | :------------------------------------------------------------------------ | :-------------------------------- |
| `signaling_proxy_sessions`                        | Gauge     | 0.4.0     | The current number of sessions                                            |                                   |
| `signaling_proxy_sessions_total`                  | Counter   | 0.4.0     | The total number of created sessions                                      |                                   |
| `signaling_proxy_sessions_resumed_total`          | Counter   | 0.4.0     | The total number of resumed sessions                                      |                                   |
| `signaling_proxy_publishers`                      | Gauge     | 0.4.0     | The current number of publishers                                          | `type`                            |
| `signaling_proxy_publishers_total`                | Counter   | 0.4.0     | The total number of created publishers                                    | `type`                            |
| `signaling_proxy_subscribers`                     | Gauge     | 0.4.0     | The current number of subscribers                                         | `type`                            |
| `signaling_proxy_subscribers_total`               | Counter   | 0.4.0     | The total number of created subscribers                                   | `type`                            |
| `signaling_proxy_command_messages_total`          | Counter   | 0.4.0     | The total number of command messages                                      | `type`                            |
| `signaling_proxy_payload_messages_total`          | Counter   | 0.4.0     | The total number of payload messages                                      | `type`                            |
| `signaling_proxy_token_errors_total`              | Counter   | 0.4.0     | The total number of token errors                                          | `reason`                          |
| `signaling_backend_session_limit`                 | Gauge     | 2.0.0     | The session limit of a backend (if set)                                   | `backend`                         |
| `signaling_backend_session_limit_exceeded_total`  | Counter   | 0.4.0     | The number of times the session limit exceeded                            | `backend`                         |
| `signaling_backend_current`                       | Gauge     | 0.4.0     | The current number of configured backends                                 |                                   |
| `signaling_client_countries_total`                | Counter   | 0.4.0     | The total number of connections by country                                | `country`                         |
| `signaling_hub_rooms`                             | Gauge     | 0.4.0     | The current number of rooms per backend                                   | `backend`                         |
| `signaling_hub_sessions`                          | Gauge     | 0.4.0     | The current number of sessions per backend                                | `backend`, `clienttype`           |
| `signaling_hub_sessions_total`                    | Counter   | 0.4.0     | The total number of sessions per backend                                  | `backend`, `clienttype`           |
| `signaling_hub_sessions_resume_total`             | Counter   | 0.4.0     | The total number of resumed sessions per backend                          | `backend`, `clienttype`           |
| `signaling_hub_sessions_resume_failed_total`      | Counter   | 0.4.0     | The total number of failed session resume requests                        |                                   |
| `signaling_mcu_publishers`                        | Gauge     | 0.4.0     | The current number of publishers                                          | `type`                            |
| `signaling_mcu_publishers_total`                  | Counter   | 0.4.0     | The total number of created publishers                                    | `type`                            |
| `signaling_mcu_subscribers`                       | Gauge     | 0.4.0     | The current number of subscribers                                         | `type`                            |
| `signaling_mcu_subscribers_total`                 | Counter   | 0.4.0     | The total number of created subscribers                                   | `type`                            |
| `signaling_mcu_nopublisher_total`                 | Counter   | 0.4.0     | The total number of subscribe requests where no publisher exists          | `type`                            |
| `signaling_mcu_messages_total`                    | Counter   | 0.4.0     | The total number of MCU messages                                          | `type`                            |
| `signaling_mcu_publisher_streams`                 | Gauge     | 0.4.0     | The current number of published media streams                             | `type`                            |
| `signaling_mcu_subscriber_streams`                | Gauge     | 0.4.0     | The current number of subscribed media streams                            | `type`                            |
| `signaling_mcu_backend_connections`               | Gauge     | 0.4.0     | Current number of connections to signaling proxy backends                 | `country`                         |
| `signaling_mcu_backend_load`                      | Gauge     | 0.4.0     | Current load of signaling proxy backends                                  | `url`                             |
| `signaling_mcu_no_backend_available_total`        | Counter   | 0.4.0     | Total number of publishing requests where no backend was available        | `type`                            |
| `signaling_room_sessions`                         | Gauge     | 0.4.0     | The current number of sessions in a room                                  | `backend`, `room`, `clienttype`   |
| `signaling_server_messages_total`                 | Counter   | 0.4.0     | The total number of signaling messages                                    | `type`                            |
| `signaling_grpc_clients`                          | Gauge     | 1.0.0     | The current number of GRPC clients                                        |                                   |
| `signaling_grpc_client_calls_total`               | Counter   | 1.0.0     | The total number of GRPC client calls                                     | `method`                          |
| `signaling_grpc_server_calls_total`               | Counter   | 1.0.0     | The total number of GRPC server calls                                     | `method`                          |
| `signaling_http_client_pool_connections`          | Gauge     | 1.2.4     | The current number of HTTP client connections per host                    | `host`                            |
| `signaling_throttle_delayed_total`                | Counter   | 1.2.5     | The total number of delayed requests                                      | `action`, `delay`                 |
| `signaling_throttle_bruteforce_total`             | Counter   | 1.2.5     | The total number of rejected bruteforce requests                          | `action`                          |
| `signaling_backend_client_requests_total`         | Counter   | 2.0.3     | The total number of backend client requests                               | `backend`                         |
| `signaling_backend_client_requests_duration`      | Histogram | 2.0.3     | The duration of backend client requests in seconds                        | `backend`                         |
| `signaling_backend_client_requests_errors_total`  | Counter   | 2.0.3     | The total number of backend client requests that had an error             | `backend`, `error`                |
| `signaling_mcu_bandwidth`                         | Gauge     | 2.0.5     | The current bandwidth in bytes per second                                 | `direction`                       |
| `signaling_mcu_backend_usage`                     | Gauge     | 2.0.5     | The current usage of signaling proxy backends in percent                  | `url`, `direction`                |
| `signaling_mcu_backend_bandwidth`                 | Gauge     | 2.0.5     | The current bandwidth of signaling proxy backends in bytes per second     | `url`, `direction`                |
| `signaling_proxy_load`                            | Gauge     | 2.0.5     | The current load of the signaling proxy                                   |                                   |
| `signaling_client_rtt`                            | Histogram | 2.0.5     | The roundtrip time of WebSocket ping messages in milliseconds             |                                   |
| `signaling_mcu_selected_local_candidate_total`    | Counter   | 2.0.5     | Total number of selected local candidates                                 | `type`, `transport`, `family`     |
| `signaling_mcu_selected_remote_candidate_total`   | Counter   | 2.0.5     | Total number of selected local candidates                                 | `type`, `transport`, `family`     |
| `signaling_mcu_peerconnection_state_total`        | Counter   | 2.0.5     | Total number PeerConnection states                                        | `state`, `reason`                 |
| `signaling_mcu_ice_state_total`                   | Counter   | 2.0.5     | Total number of ICE connection states                                     | `state`                           |
| `signaling_mcu_dtls_state_total`                  | Counter   | 2.0.5     | Total number of DTLS connection states                                    | `state`                           |
