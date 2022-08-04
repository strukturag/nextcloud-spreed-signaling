# Changelog

All notable changes to this project will be documented in this file.

## 1.0.0 - 2022-08-04

### Added
- Clustering support.
  [#281](https://github.com/strukturag/nextcloud-spreed-signaling/pull/281)
- Send initial "welcome" message when clients connect.
  [#288](https://github.com/strukturag/nextcloud-spreed-signaling/pull/288)
- Support hello auth version "2.0" with JWT.
  [#251](https://github.com/strukturag/nextcloud-spreed-signaling/pull/251)
- dist: add systemd sysusers file.
  [#275](https://github.com/strukturag/nextcloud-spreed-signaling/pull/275)
- Add more tests.
  [#292](https://github.com/strukturag/nextcloud-spreed-signaling/pull/292)
- Add tests for virtual sessions.
  [#295](https://github.com/strukturag/nextcloud-spreed-signaling/pull/295)
- Implement per-backend session limit for clusters.
  [#296](https://github.com/strukturag/nextcloud-spreed-signaling/pull/296)

### Changed
- Don't run "go mod tidy" when building.
  [#269](https://github.com/strukturag/nextcloud-spreed-signaling/pull/269)
- Bump sphinx from 5.0.0 to 5.0.1 in /docs
  [#270](https://github.com/strukturag/nextcloud-spreed-signaling/pull/270)
- Bump sphinx from 5.0.1 to 5.0.2 in /docs
  [#277](https://github.com/strukturag/nextcloud-spreed-signaling/pull/277)
- Move common etcd code to own class.
  [#282](https://github.com/strukturag/nextcloud-spreed-signaling/pull/282)
- Support arbitrary capabilities values.
  [#287](https://github.com/strukturag/nextcloud-spreed-signaling/pull/287)
- dist: harden systemd service unit.
  [#276](https://github.com/strukturag/nextcloud-spreed-signaling/pull/276)
- Update to Go module version of github.com/golang-jwt/jwt
  [#289](https://github.com/strukturag/nextcloud-spreed-signaling/pull/289)
- Disconnect sessions with the same room session id synchronously.
  [#294](https://github.com/strukturag/nextcloud-spreed-signaling/pull/294)
- Bump google.golang.org/grpc from 1.47.0 to 1.48.0
  [#297](https://github.com/strukturag/nextcloud-spreed-signaling/pull/297)
- Update to github.com/pion/sdp v3.0.5
  [#301](https://github.com/strukturag/nextcloud-spreed-signaling/pull/301)
- Bump sphinx from 5.0.2 to 5.1.1 in /docs
  [#303](https://github.com/strukturag/nextcloud-spreed-signaling/pull/303)
- make: Include vendored dependencies in tarball.
  [#300](https://github.com/strukturag/nextcloud-spreed-signaling/pull/300)
- docs: update and pin dependencies.
  [#305](https://github.com/strukturag/nextcloud-spreed-signaling/pull/305)
- Bump actions/upload-artifact from 2 to 3
  [#307](https://github.com/strukturag/nextcloud-spreed-signaling/pull/307)
- Bump actions/download-artifact from 2 to 3
  [#308](https://github.com/strukturag/nextcloud-spreed-signaling/pull/308)
- Bump google.golang.org/protobuf from 1.28.0 to 1.28.1
  [#306](https://github.com/strukturag/nextcloud-spreed-signaling/pull/306)
- CI: Also test with Golang 1.19
  [#310](https://github.com/strukturag/nextcloud-spreed-signaling/pull/310)

### Fixed
- Fix check for async room messages received while not joined to a room.
  [#274](https://github.com/strukturag/nextcloud-spreed-signaling/pull/274)
- Fix testing etcd server not starting up if etcd is running on host.
  [#283](https://github.com/strukturag/nextcloud-spreed-signaling/pull/283)
- Fix CI issues on slow CPUs.
  [#290](https://github.com/strukturag/nextcloud-spreed-signaling/pull/290)
- Fix handling of "unshareScreen" messages and add test.
  [#293](https://github.com/strukturag/nextcloud-spreed-signaling/pull/293)
- Fix Read The Ddocs builds.
  [#302](https://github.com/strukturag/nextcloud-spreed-signaling/pull/302)


## 0.5.0 - 2022-06-02

### Added
- Add API documentation (previously in https://github.com/nextcloud/spreed)
  [#194](https://github.com/strukturag/nextcloud-spreed-signaling/pull/194)
- CI: Enable gofmt linter.
  [#196](https://github.com/strukturag/nextcloud-spreed-signaling/pull/196)
- CI: Enable revive linter.
  [#197](https://github.com/strukturag/nextcloud-spreed-signaling/pull/197)
- Add API for transient room data.
  [#193](https://github.com/strukturag/nextcloud-spreed-signaling/pull/193)
- Send updated offers to subscribers after publisher renegotiations.
  [#195](https://github.com/strukturag/nextcloud-spreed-signaling/pull/195)
- Add documentation on the available metrics.
  [#210](https://github.com/strukturag/nextcloud-spreed-signaling/pull/210)
- Add special events to update "incall" flags of all sessions.
  [#208](https://github.com/strukturag/nextcloud-spreed-signaling/pull/208)
- CI: Also test with Golang 1.18.
  [#209](https://github.com/strukturag/nextcloud-spreed-signaling/pull/209)
- Support DNS discovery for proxy server URLs.
  [#214](https://github.com/strukturag/nextcloud-spreed-signaling/pull/214)
- CI: Build docker image.
  [#238](https://github.com/strukturag/nextcloud-spreed-signaling/pull/238)
- Add specific id for connections and replace "update" parameter with it.
  [#229](https://github.com/strukturag/nextcloud-spreed-signaling/pull/229)
- Add "permission" for sessions that may not receive display names.
  [#227](https://github.com/strukturag/nextcloud-spreed-signaling/pull/227)
- Add support for request offers to update subscriber connections.
  [#191](https://github.com/strukturag/nextcloud-spreed-signaling/pull/191)
- Support toggling audio/video in subscribed streams.
  [#239](https://github.com/strukturag/nextcloud-spreed-signaling/pull/239)
- CI: Test building coturn/janus Docker images.
  [#258](https://github.com/strukturag/nextcloud-spreed-signaling/pull/258)
- Add command bot for "/rebase".
  [#260](https://github.com/strukturag/nextcloud-spreed-signaling/pull/260)
- Add Go Report card.
  [#262](https://github.com/strukturag/nextcloud-spreed-signaling/pull/262)
- Combine ping requests of different rooms on the same backend.
  [#250](https://github.com/strukturag/nextcloud-spreed-signaling/pull/250)

### Changed
- Bump github.com/gorilla/websocket from 1.4.2 to 1.5.0
  [#198](https://github.com/strukturag/nextcloud-spreed-signaling/pull/198)
- Bump golangci/golangci-lint-action from 2.5.2 to 3.1.0
  [#202](https://github.com/strukturag/nextcloud-spreed-signaling/pull/202)
- Bump actions/checkout from 2.4.0 to 3
  [#205](https://github.com/strukturag/nextcloud-spreed-signaling/pull/205)
- Bump actions/cache from 2.1.7 to 3
  [#211](https://github.com/strukturag/nextcloud-spreed-signaling/pull/211)
- Return dedicated error if proxy receives token that is not valid yet.
  [#212](https://github.com/strukturag/nextcloud-spreed-signaling/pull/212)
- CI: Only run workflows if relevant files have changed.
  [#218](https://github.com/strukturag/nextcloud-spreed-signaling/pull/218)
- Bump sphinx from 4.2.0 to 4.5.0 in /docs
  [#216](https://github.com/strukturag/nextcloud-spreed-signaling/pull/216)
- Bump github.com/oschwald/maxminddb-golang from 1.8.0 to 1.9.0
  [#213](https://github.com/strukturag/nextcloud-spreed-signaling/pull/213)
- Only support last two versions of Golang (1.17 / 1.18).
  [#219](https://github.com/strukturag/nextcloud-spreed-signaling/pull/219)
- Bump github.com/golang-jwt/jwt from 3.2.1+incompatible to 3.2.2+incompatible
  [#161](https://github.com/strukturag/nextcloud-spreed-signaling/pull/161)
- Bump github.com/nats-io/nats-server/v2 from 2.2.6 to 2.7.4
  [#207](https://github.com/strukturag/nextcloud-spreed-signaling/pull/207)
- Update etcd to v3.5.1
  [#179](https://github.com/strukturag/nextcloud-spreed-signaling/pull/179)
- Bump github.com/prometheus/client_golang from 1.11.0 to 1.12.1
  [#190](https://github.com/strukturag/nextcloud-spreed-signaling/pull/190)
- Bump go.etcd.io/etcd/client/v3 from 3.5.1 to 3.5.2
  [#222](https://github.com/strukturag/nextcloud-spreed-signaling/pull/222)
- Use features from newer Golang versions.
  [#220](https://github.com/strukturag/nextcloud-spreed-signaling/pull/220)
- Bump actions/setup-go from 2 to 3
  [#226](https://github.com/strukturag/nextcloud-spreed-signaling/pull/226)
- Send directly to local session with disconnected client.
  [#228](https://github.com/strukturag/nextcloud-spreed-signaling/pull/228)
- Bump github.com/nats-io/nats-server/v2 from 2.7.4 to 2.8.1
  [#234](https://github.com/strukturag/nextcloud-spreed-signaling/pull/234)
- Bump go.etcd.io/etcd/client/pkg/v3 from 3.5.2 to 3.5.4
  [#235](https://github.com/strukturag/nextcloud-spreed-signaling/pull/235)
- Bump github/codeql-action from 1 to 2
  [#237](https://github.com/strukturag/nextcloud-spreed-signaling/pull/237)
- Bump go.etcd.io/etcd/client/v3 from 3.5.2 to 3.5.4
  [#236](https://github.com/strukturag/nextcloud-spreed-signaling/pull/236)
- Bump github.com/nats-io/nats-server/v2 from 2.8.1 to 2.8.2
  [#242](https://github.com/strukturag/nextcloud-spreed-signaling/pull/242)
- Bump docker/setup-buildx-action from 1 to 2
  [#245](https://github.com/strukturag/nextcloud-spreed-signaling/pull/245)
- Bump docker/build-push-action from 2 to 3
  [#244](https://github.com/strukturag/nextcloud-spreed-signaling/pull/244)
- Bump github.com/nats-io/nats.go from 1.14.0 to 1.15.0
  [#243](https://github.com/strukturag/nextcloud-spreed-signaling/pull/243)
- Bump readthedocs-sphinx-search from 0.1.1 to 0.1.2 in /docs
  [#248](https://github.com/strukturag/nextcloud-spreed-signaling/pull/248)
- CI: Run when workflow yaml file has changed.
  [#249](https://github.com/strukturag/nextcloud-spreed-signaling/pull/249)
- Bump golangci/golangci-lint-action from 3.1.0 to 3.2.0
  [#247](https://github.com/strukturag/nextcloud-spreed-signaling/pull/247)
- Move capabilities handling to own file and refactor http client pool.
  [#252](https://github.com/strukturag/nextcloud-spreed-signaling/pull/252)
- Increase allowed body size for backend requests.
  [#255](https://github.com/strukturag/nextcloud-spreed-signaling/pull/255)
- Improve test coverage.
  [#253](https://github.com/strukturag/nextcloud-spreed-signaling/pull/253)
- Switch to official Coturn docker image.
  [#259](https://github.com/strukturag/nextcloud-spreed-signaling/pull/259)
- Bump github.com/prometheus/client_golang from 1.12.1 to 1.12.2
  [#256](https://github.com/strukturag/nextcloud-spreed-signaling/pull/256)
- Update Dockerfile versions.
  [#257](https://github.com/strukturag/nextcloud-spreed-signaling/pull/257)
- Update Alpine to 3.15 version, fix CVE-2022-28391
  [#261](https://github.com/strukturag/nextcloud-spreed-signaling/pull/261)
- Bump cirrus-actions/rebase from 1.6 to 1.7
  [#263](https://github.com/strukturag/nextcloud-spreed-signaling/pull/263)
- Bump github.com/nats-io/nats.go from 1.15.0 to 1.16.0
  [#267](https://github.com/strukturag/nextcloud-spreed-signaling/pull/267)
- Bump jandelgado/gcov2lcov-action from 1.0.8 to 1.0.9
  [#264](https://github.com/strukturag/nextcloud-spreed-signaling/pull/264)
- Bump github.com/nats-io/nats-server/v2 from 2.8.2 to 2.8.4
  [#266](https://github.com/strukturag/nextcloud-spreed-signaling/pull/266)
- Bump sphinx from 4.5.0 to 5.0.0 in /docs
  [#268](https://github.com/strukturag/nextcloud-spreed-signaling/pull/268)

### Fixed
- CI: Fix linter errors.
  [#206](https://github.com/strukturag/nextcloud-spreed-signaling/pull/206)
- CI: Pin dependencies to fix readthedocs build.
  [#215](https://github.com/strukturag/nextcloud-spreed-signaling/pull/215)
- Fix mediaType not updated after publisher renegotiations.
  [#221](https://github.com/strukturag/nextcloud-spreed-signaling/pull/221)
- Fix "signaling_server_messages_total" stat not being incremented.
  [#190](https://github.com/strukturag/nextcloud-spreed-signaling/pull/190)


## 0.4.1 - 2022-01-25

### Added
- The room session id is included in "joined" events.
  [#178](https://github.com/strukturag/nextcloud-spreed-signaling/pull/178)
- Clients can provide the maximum publishing bandwidth in offer requests.
  [#183](https://github.com/strukturag/nextcloud-spreed-signaling/pull/183)

### Changed
- Change source of country -> continent map.
  [#177](https://github.com/strukturag/nextcloud-spreed-signaling/pull/177)
- Bump actions/cache from 2.1.6 to 2.1.7
  [#171](https://github.com/strukturag/nextcloud-spreed-signaling/pull/171)


## 0.4.0 - 2021-11-10

### Added
- Support continent mapping overrides.
  [#143](https://github.com/strukturag/nextcloud-spreed-signaling/pull/143)
- Add prometheus metrics
  [#99](https://github.com/strukturag/nextcloud-spreed-signaling/pull/99)
- Support separate permissions for publishing audio / video.
  [#157](https://github.com/strukturag/nextcloud-spreed-signaling/pull/157)
- Check individual audio/video permissions on change.
  [#169](https://github.com/strukturag/nextcloud-spreed-signaling/pull/169)
- CI: Also test with Go 1.17
  [#153](https://github.com/strukturag/nextcloud-spreed-signaling/pull/153)

### Changed
- Force HTTPS for backend connections in old-style configurations.
  [#132](https://github.com/strukturag/nextcloud-spreed-signaling/pull/132)
- Only include body in 307/308 redirects if going to same host
  [#134](https://github.com/strukturag/nextcloud-spreed-signaling/pull/134)
- Stop publishers if session is no longer allowed to publish.
  [#140](https://github.com/strukturag/nextcloud-spreed-signaling/pull/140)
- Only allow subscribing if both users are in the same room and call.
  [#133](https://github.com/strukturag/nextcloud-spreed-signaling/pull/133)
- Internal clients always may subscribe all streams.
  [#159](https://github.com/strukturag/nextcloud-spreed-signaling/pull/159)
- Reduce RTT logging
  [#167](https://github.com/strukturag/nextcloud-spreed-signaling/pull/167)
- deps: Migrate to "github.com/golang-jwt/jwt".
  [#160](https://github.com/strukturag/nextcloud-spreed-signaling/pull/160)
- Bump coverallsapp/github-action from 1.1.2 to 1.1.3
  [#131](https://github.com/strukturag/nextcloud-spreed-signaling/pull/131)
- Bump github.com/google/uuid from 1.2.0 to 1.3.0
  [#138](https://github.com/strukturag/nextcloud-spreed-signaling/pull/138)
- Bump github.com/prometheus/client_golang from 1.10.0 to 1.11.0
  [#144](https://github.com/strukturag/nextcloud-spreed-signaling/pull/144)
- Bump github.com/nats-io/nats.go from 1.11.0 to 1.12.1
  [#150](https://github.com/strukturag/nextcloud-spreed-signaling/pull/150)
- Bump github.com/nats-io/nats.go from 1.12.1 to 1.12.3
  [#154](https://github.com/strukturag/nextcloud-spreed-signaling/pull/154)
- Bump github.com/nats-io/nats.go from 1.12.3 to 1.13.0
  [#158](https://github.com/strukturag/nextcloud-spreed-signaling/pull/158)
- Bump actions/checkout from 2.3.4 to 2.3.5
  [#163](https://github.com/strukturag/nextcloud-spreed-signaling/pull/163)
- Bump actions/checkout from 2.3.5 to 2.4.0
  [#166](https://github.com/strukturag/nextcloud-spreed-signaling/pull/166)

### Fixed
- Adjusted easyjson for multiarch builds
  [#129](https://github.com/strukturag/nextcloud-spreed-signaling/pull/129)


## 0.3.0 - 2021-07-01

### Added
- Certificate validation can be disabled for proxy connections
- Number of sessions per backend can be limited
  [#67](https://github.com/strukturag/nextcloud-spreed-signaling/pull/67)
- Use Go modules for dependency tracking, drop support for Golang < 1.13
  [#88](https://github.com/strukturag/nextcloud-spreed-signaling/pull/88)
- Support defining maximum bandwidths at diferent levels
  [#76](https://github.com/strukturag/nextcloud-spreed-signaling/pull/76)
- Show coverage report in PRs
  [#34](https://github.com/strukturag/nextcloud-spreed-signaling/pull/34)
- CI: Also test with Golang 1.16
- CI: Run golint
  [#32](https://github.com/strukturag/nextcloud-spreed-signaling/pull/32)
- CI: Add CodeQL analysis
  [#112](https://github.com/strukturag/nextcloud-spreed-signaling/pull/112)
- Add tests for regular NATS client
  [#105](https://github.com/strukturag/nextcloud-spreed-signaling/pull/105)
- Fetch capabilities to check if "v3" signaling API of Talk should be used.
  [#119](https://github.com/strukturag/nextcloud-spreed-signaling/pull/119)
- Add API to select a simulcast substream / temporal layer
  [#104](https://github.com/strukturag/nextcloud-spreed-signaling/pull/104)

### Changed
- Improved detection of broken connections between server and proxy
  [#65](https://github.com/strukturag/nextcloud-spreed-signaling/pull/65)
- Stop using legacy ptype `listener`
  [#83](https://github.com/strukturag/nextcloud-spreed-signaling/pull/83)
- Update gorilla/mux to 1.8.0
  [#89](https://github.com/strukturag/nextcloud-spreed-signaling/pull/89)
- Remove unnecessary dependency golang.org/x/net
  [#90](https://github.com/strukturag/nextcloud-spreed-signaling/pull/90)
- Update nats.go to 1.10.0
  [#92](https://github.com/strukturag/nextcloud-spreed-signaling/pull/92)
- Update maxminddb-golang to 1.8.0
  [#91](https://github.com/strukturag/nextcloud-spreed-signaling/pull/91)
- Add dependabot integration
  [#93](https://github.com/strukturag/nextcloud-spreed-signaling/pull/93)
- Bump github.com/google/uuid from 1.1.2 to 1.2.0
  [#94](https://github.com/strukturag/nextcloud-spreed-signaling/pull/94)
- Bump github.com/gorilla/websocket from 1.2.0 to 1.4.2
  [#95](https://github.com/strukturag/nextcloud-spreed-signaling/pull/95)
- Remove deprecated github.com/gorilla/context
- Update to go.etcd.io/etcd 3.4.15
- make: Cache easyjson results.
  [#96](https://github.com/strukturag/nextcloud-spreed-signaling/pull/96)
- Various updates to Docker components
  [#78](https://github.com/strukturag/nextcloud-spreed-signaling/pull/78)
- Bump coverallsapp/github-action from v1.1.1 to v1.1.2
  [#102](https://github.com/strukturag/nextcloud-spreed-signaling/pull/102)
- Bump jandelgado/gcov2lcov-action from v1.0.2 to v1.0.8
  [#103](https://github.com/strukturag/nextcloud-spreed-signaling/pull/103)
- Bump actions/cache from 2 to 2.1.5
  [#106](https://github.com/strukturag/nextcloud-spreed-signaling/pull/106)
- Bump golangci/golangci-lint-action from 2 to 2.5.2
  [#107](https://github.com/strukturag/nextcloud-spreed-signaling/pull/107)
- Bump actions/checkout from 2 to 2.3.4
  [#108](https://github.com/strukturag/nextcloud-spreed-signaling/pull/108)
- Bump actions/cache from 2.1.5 to 2.1.6
  [#110](https://github.com/strukturag/nextcloud-spreed-signaling/pull/110)
- Don't log TURN credentials
  [#113](https://github.com/strukturag/nextcloud-spreed-signaling/pull/113)
- Remove NATS notifications for Janus publishers
  [#114](https://github.com/strukturag/nextcloud-spreed-signaling/pull/114)
- Make client processing asynchronous
  [#111](https://github.com/strukturag/nextcloud-spreed-signaling/pull/111)
- Bump github.com/nats-io/nats-server/v2 from 2.2.1 to 2.2.6
  [#116](https://github.com/strukturag/nextcloud-spreed-signaling/pull/116)
- Notify new clients about flags of virtual sessions
  [#121](https://github.com/strukturag/nextcloud-spreed-signaling/pull/121)

### Fixed
- Adjusted godeps for multiarch builds
  [#69](https://github.com/strukturag/nextcloud-spreed-signaling/pull/69)
- Add missing lock when accessing internal sessions map
- Fixed parallel building
  [#73](https://github.com/strukturag/nextcloud-spreed-signaling/pull/73)
- Make the response from the client auth backend OCS compliant
  [#74](https://github.com/strukturag/nextcloud-spreed-signaling/pull/74)
- Fixed alignment of 64bit members that are accessed atomically
  [#72](https://github.com/strukturag/nextcloud-spreed-signaling/pull/72)
- Only build "godep" binary once
  [#75](https://github.com/strukturag/nextcloud-spreed-signaling/pull/75)
- Update config example for Apache proxy config
  [#82](https://github.com/strukturag/nextcloud-spreed-signaling/pull/82)
- Remove remaining virtual sessions if client session is closed
- Fix Caddy v2 example config
  [#97](https://github.com/strukturag/nextcloud-spreed-signaling/pull/97)
- Fix various issues found by golangci-lint
  [#100](https://github.com/strukturag/nextcloud-spreed-signaling/pull/100)
- Support multiple waiters for the same key
  [#120](https://github.com/strukturag/nextcloud-spreed-signaling/pull/120)
- Various test improvements / fixes
  [#115](https://github.com/strukturag/nextcloud-spreed-signaling/pull/115)


## 0.2.0 - 2020-12-08

### Added
- Reload backends from configuration on SIGHUP
  [#52](https://github.com/strukturag/nextcloud-spreed-signaling/pull/52)
  [#53](https://github.com/strukturag/nextcloud-spreed-signaling/pull/53)
- Add support for virtual sessions
  [#61](https://github.com/strukturag/nextcloud-spreed-signaling/pull/61)

### Changed
- Default to proxy url type "static" if none is configured
- Don't perform request to proxy if context is already done
- Mark session as used when proxy connection is interrupted to prevent
  from timing out too early
- Use dedicated (shorter) timeout for proxy requests to avoid using the whole
  available timeout for the first proxy request
- Update logging when creating / deleting publishers / subscribers
- Include load in stats response
- Send MCU messages through the session
  [#55](https://github.com/strukturag/nextcloud-spreed-signaling/pull/55)
- Add '--full-trickle' to janus command
  [#57](https://github.com/strukturag/nextcloud-spreed-signaling/pull/57)
- README: Add missing information for creating group
  [#60](https://github.com/strukturag/nextcloud-spreed-signaling/pull/60)
- Canonicalize all URLs before comparisons / lookups
  [#62](https://github.com/strukturag/nextcloud-spreed-signaling/pull/62)

### Fixed
- Handle case where etcd cluster is not available during startup
- Remove duplicate argument in Dockerfile
  [#50](https://github.com/strukturag/nextcloud-spreed-signaling/pull/50)
- Handle old-style MCU configuration with type but no url
- Fix proxy client cleanup code
  [#56](https://github.com/strukturag/nextcloud-spreed-signaling/pull/56)


## 0.1.0 - 2020-09-07

### Added
- Add Docker support
  [#7](https://github.com/strukturag/nextcloud-spreed-signaling/pull/7)
- Added basic stats API
  [#16](https://github.com/strukturag/nextcloud-spreed-signaling/pull/16)
- Add "reason" field to disinvite messages
  [#26](https://github.com/strukturag/nextcloud-spreed-signaling/pull/26)
- Added support for multiple Nextcloud backends
  [#28](https://github.com/strukturag/nextcloud-spreed-signaling/pull/28)
- Support connecting to multiple Janus servers
  [#36](https://github.com/strukturag/nextcloud-spreed-signaling/pull/36)
- Added support for loading proxy tokens from etcd cluser
  [#44](https://github.com/strukturag/nextcloud-spreed-signaling/pull/44)
- Proxy URLs are reloaded on SIGHUP
  [#46](https://github.com/strukturag/nextcloud-spreed-signaling/pull/46)
- Added support for loading proxy URls from etcd cluster
  [#47](https://github.com/strukturag/nextcloud-spreed-signaling/pull/47)
- Add option to override GeoIP lookups (e.g. for local addresses)
  [#48](https://github.com/strukturag/nextcloud-spreed-signaling/pull/48)

### Changed
- The continent map is no longer downloaded on each build
  [#29](https://github.com/strukturag/nextcloud-spreed-signaling/pull/29)
- NATS messages are processed directly
  [#35](https://github.com/strukturag/nextcloud-spreed-signaling/pull/35)
- Support changed "slowlink" message from Janus > 0.7.3
  [#39](https://github.com/strukturag/nextcloud-spreed-signaling/pull/39)
- The GeoIP database can be loaded from a local file
  [#40](https://github.com/strukturag/nextcloud-spreed-signaling/pull/40)
- Drop support for Golang < 1.10.

### Fixed
- Fixes for building on FreeBSD
  [#2](https://github.com/strukturag/nextcloud-spreed-signaling/pull/2)
- Fixes for typos in comments and error messages
  [#10](https://github.com/strukturag/nextcloud-spreed-signaling/pull/10)
- Remove credentials from log
  [#13](https://github.com/strukturag/nextcloud-spreed-signaling/pull/13)

### Documentation
- Add systemd to docs
  [#3](https://github.com/strukturag/nextcloud-spreed-signaling/pull/3)
- Add caddy server to reverse proxy examples
  [#5](https://github.com/strukturag/nextcloud-spreed-signaling/pull/5)
- Update link to API documentation
  [#6](https://github.com/strukturag/nextcloud-spreed-signaling/pull/6)
- Update build requirements
  [#12](https://github.com/strukturag/nextcloud-spreed-signaling/pull/12)


## 0.0.13 - 2020-05-12

- Initial OpenSource version.
