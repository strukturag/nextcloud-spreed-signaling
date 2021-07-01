# Changelog

All notable changes to this project will be documented in this file.

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
