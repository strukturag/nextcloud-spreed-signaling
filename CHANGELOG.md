# Changelog

All notable changes to this project will be documented in this file.

## 1.2.3 - 2024-01-31

### Added
- CI: Check license headers.
  [#627](https://github.com/strukturag/nextcloud-spreed-signaling/pull/627)
- Add "welcome" endpoint to proxy.
  [#644](https://github.com/strukturag/nextcloud-spreed-signaling/pull/644)

### Changed
- build(deps): Bump github/codeql-action from 2 to 3
  [#619](https://github.com/strukturag/nextcloud-spreed-signaling/pull/619)
- build(deps): Bump github.com/google/uuid from 1.4.0 to 1.5.0
  [#618](https://github.com/strukturag/nextcloud-spreed-signaling/pull/618)
- build(deps): Bump google.golang.org/grpc from 1.59.0 to 1.60.0
  [#617](https://github.com/strukturag/nextcloud-spreed-signaling/pull/617)
- build(deps): Bump the artifacts group with 2 updates
  [#622](https://github.com/strukturag/nextcloud-spreed-signaling/pull/622)
- build(deps): Bump golang.org/x/crypto from 0.16.0 to 0.17.0
  [#623](https://github.com/strukturag/nextcloud-spreed-signaling/pull/623)
- build(deps): Bump google.golang.org/grpc from 1.60.0 to 1.60.1
  [#624](https://github.com/strukturag/nextcloud-spreed-signaling/pull/624)
- Refactor proxy config
  [#606](https://github.com/strukturag/nextcloud-spreed-signaling/pull/606)
- build(deps): Bump google.golang.org/protobuf from 1.31.0 to 1.32.0
  [#629](https://github.com/strukturag/nextcloud-spreed-signaling/pull/629)
- build(deps): Bump github.com/prometheus/client_golang from 1.17.0 to 1.18.0
  [#630](https://github.com/strukturag/nextcloud-spreed-signaling/pull/630)
- build(deps): Bump jinja2 from 3.1.2 to 3.1.3 in /docs
  [#632](https://github.com/strukturag/nextcloud-spreed-signaling/pull/632)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.10.7 to 2.10.9
  [#633](https://github.com/strukturag/nextcloud-spreed-signaling/pull/633)
- build(deps): Bump markdown from 3.5.1 to 3.5.2 in /docs
  [#631](https://github.com/strukturag/nextcloud-spreed-signaling/pull/631)
- build(deps): Bump github.com/nats-io/nats.go from 1.31.0 to 1.32.0
  [#634](https://github.com/strukturag/nextcloud-spreed-signaling/pull/634)
- build(deps): Bump readthedocs-sphinx-search from 0.3.1 to 0.3.2 in /docs
  [#635](https://github.com/strukturag/nextcloud-spreed-signaling/pull/635)
- build(deps): Bump actions/cache from 3 to 4
  [#638](https://github.com/strukturag/nextcloud-spreed-signaling/pull/638)
- build(deps): Bump github.com/google/uuid from 1.5.0 to 1.6.0
  [#643](https://github.com/strukturag/nextcloud-spreed-signaling/pull/643)
- build(deps): Bump google.golang.org/grpc from 1.60.1 to 1.61.0
  [#645](https://github.com/strukturag/nextcloud-spreed-signaling/pull/645)
- build(deps): Bump peter-evans/create-or-update-comment from 3 to 4
  [#646](https://github.com/strukturag/nextcloud-spreed-signaling/pull/646)
- CI: No longer need to manually cache Go modules.
  [#648](https://github.com/strukturag/nextcloud-spreed-signaling/pull/648)
- CI: Disable cache for linter to bring back annotations.
  [#647](https://github.com/strukturag/nextcloud-spreed-signaling/pull/647)
- Refactor DNS monitoring
  [#648](https://github.com/strukturag/nextcloud-spreed-signaling/pull/648)

### Fixed
- Fix link to NATS install docs
  [#637](https://github.com/strukturag/nextcloud-spreed-signaling/pull/637)
- docker: Always need to set proxy token id / key for server.
  [#641](https://github.com/strukturag/nextcloud-spreed-signaling/pull/641)


## 1.2.2 - 2023-12-11

### Added
- Include "~docker" in version if built on Docker.
  [#602](https://github.com/strukturag/nextcloud-spreed-signaling/pull/602)

### Changed
- CI: No need to build docker images for testing, done internally.
  [#603](https://github.com/strukturag/nextcloud-spreed-signaling/pull/603)
- build(deps): Bump sphinx-rtd-theme from 1.3.0 to 2.0.0 in /docs
  [#604](https://github.com/strukturag/nextcloud-spreed-signaling/pull/604)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.10.5 to 2.10.6
  [#605](https://github.com/strukturag/nextcloud-spreed-signaling/pull/605)
- build(deps): Bump actions/setup-go from 4 to 5
  [#608](https://github.com/strukturag/nextcloud-spreed-signaling/pull/608)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.10.6 to 2.10.7
  [#612](https://github.com/strukturag/nextcloud-spreed-signaling/pull/612)
- build(deps): Bump the etcd group with 4 updates
  [#611](https://github.com/strukturag/nextcloud-spreed-signaling/pull/611)

### Fixed
- Skip options from default section when parsing "geoip-overrides".
  [#609](https://github.com/strukturag/nextcloud-spreed-signaling/pull/609)
- Hangup virtual session if it gets disinvited.
  [#610](https://github.com/strukturag/nextcloud-spreed-signaling/pull/610)


## 1.2.1 - 2023-11-15

### Added
- feat(scripts): Add a script to simplify the logs to make it more easily to trace a user/session
[#480](https://github.com/strukturag/nextcloud-spreed-signaling/pull/480)

### Changed
- build(deps): Bump markdown from 3.5 to 3.5.1 in /docs
  [#594](https://github.com/strukturag/nextcloud-spreed-signaling/pull/594)
- build(deps): Bump github.com/gorilla/websocket from 1.5.0 to 1.5.1
  [#595](https://github.com/strukturag/nextcloud-spreed-signaling/pull/595)
- build(deps): Bump github.com/gorilla/securecookie from 1.1.1 to 1.1.2
  [#597](https://github.com/strukturag/nextcloud-spreed-signaling/pull/597)
- build(deps): Bump github.com/gorilla/mux from 1.8.0 to 1.8.1
  [#596](https://github.com/strukturag/nextcloud-spreed-signaling/pull/596)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.10.4 to 2.10.5
  [#599](https://github.com/strukturag/nextcloud-spreed-signaling/pull/599)
- Improve support for multiple backends with dialouts
  [#592](https://github.com/strukturag/nextcloud-spreed-signaling/pull/592)
- build(deps): Bump go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc from 0.25.0 to 0.46.0
  [#600](https://github.com/strukturag/nextcloud-spreed-signaling/pull/600)


## 1.2.0 - 2023-10-30

### Added
- Use GeoIP overrides if no GeoIP database is configured.
  [#532](https://github.com/strukturag/nextcloud-spreed-signaling/pull/532)
- Log warning if no (static) backends have been configured.
  [#533](https://github.com/strukturag/nextcloud-spreed-signaling/pull/533)
- Fallback to common shared secret if none is set for backends.
  [#534](https://github.com/strukturag/nextcloud-spreed-signaling/pull/534)
- CI: Test with Golang 1.21
  [#536](https://github.com/strukturag/nextcloud-spreed-signaling/pull/536)
- Return response if session tries to join room again.
  [#547](https://github.com/strukturag/nextcloud-spreed-signaling/pull/547)
- Support TTL for transient data.
  [#575](https://github.com/strukturag/nextcloud-spreed-signaling/pull/575)
- Implement message handler for dialout support.
  [#563](https://github.com/strukturag/nextcloud-spreed-signaling/pull/563)
- No longer support Golang 1.19.
  [#580](https://github.com/strukturag/nextcloud-spreed-signaling/pull/580)

### Changed
- build(deps): Bump google.golang.org/grpc from 1.56.1 to 1.57.0
  [#520](https://github.com/strukturag/nextcloud-spreed-signaling/pull/520)
- build(deps): Bump coverallsapp/github-action from 2.2.0 to 2.2.1
  [#514](https://github.com/strukturag/nextcloud-spreed-signaling/pull/514)
- build(deps): Bump github.com/nats-io/nats.go from 1.27.1 to 1.28.0
  [#515](https://github.com/strukturag/nextcloud-spreed-signaling/pull/515)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.9.19 to 2.9.20
  [#513](https://github.com/strukturag/nextcloud-spreed-signaling/pull/513)
- build(deps): Bump mkdocs from 1.4.3 to 1.5.1 in /docs
  [#523](https://github.com/strukturag/nextcloud-spreed-signaling/pull/523)
- build(deps): Bump markdown from 3.3.7 to 3.4.4 in /docs
  [#519](https://github.com/strukturag/nextcloud-spreed-signaling/pull/519)
- build(deps): Bump mkdocs from 1.5.1 to 1.5.2 in /docs
  [#525](https://github.com/strukturag/nextcloud-spreed-signaling/pull/525)
- build(deps): Bump github.com/oschwald/maxminddb-golang from 1.11.0 to 1.12.0
  [#524](https://github.com/strukturag/nextcloud-spreed-signaling/pull/524)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.9.20 to 2.9.21
  [#530](https://github.com/strukturag/nextcloud-spreed-signaling/pull/530)
- build(deps): Bump sphinx from 6.2.1 to 7.2.4 in /docs
  [#542](https://github.com/strukturag/nextcloud-spreed-signaling/pull/542)
- build(deps): Bump github.com/google/uuid from 1.3.0 to 1.3.1
  [#539](https://github.com/strukturag/nextcloud-spreed-signaling/pull/539)
- build(deps): Bump sphinx from 7.2.4 to 7.2.5 in /docs
  [#544](https://github.com/strukturag/nextcloud-spreed-signaling/pull/544)
- build(deps): Bump coverallsapp/github-action from 2.2.1 to 2.2.2
  [#546](https://github.com/strukturag/nextcloud-spreed-signaling/pull/546)
- build(deps): Bump actions/checkout from 3 to 4
  [#545](https://github.com/strukturag/nextcloud-spreed-signaling/pull/545)
- build(deps): Bump google.golang.org/grpc from 1.57.0 to 1.58.0
  [#549](https://github.com/strukturag/nextcloud-spreed-signaling/pull/549)
- build(deps): Bump docker/metadata-action from 4 to 5
  [#552](https://github.com/strukturag/nextcloud-spreed-signaling/pull/552)
- build(deps): Bump docker/setup-qemu-action from 2 to 3
  [#553](https://github.com/strukturag/nextcloud-spreed-signaling/pull/553)
- build(deps): Bump docker/login-action from 2 to 3
  [#554](https://github.com/strukturag/nextcloud-spreed-signaling/pull/554)
- build(deps): Bump docker/setup-buildx-action from 2 to 3
  [#555](https://github.com/strukturag/nextcloud-spreed-signaling/pull/555)
- build(deps): Bump coverallsapp/github-action from 2.2.2 to 2.2.3
  [#551](https://github.com/strukturag/nextcloud-spreed-signaling/pull/551)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.9.21 to 2.9.22
  [#550](https://github.com/strukturag/nextcloud-spreed-signaling/pull/550)
- build(deps): Bump docker/build-push-action from 4 to 5
  [#557](https://github.com/strukturag/nextcloud-spreed-signaling/pull/557)
- build(deps): Bump github.com/nats-io/nats.go from 1.28.0 to 1.29.0
  [#558](https://github.com/strukturag/nextcloud-spreed-signaling/pull/558)
- build(deps): Bump google.golang.org/grpc from 1.58.0 to 1.58.1
  [#559](https://github.com/strukturag/nextcloud-spreed-signaling/pull/559)
- build(deps): Bump sphinx from 7.2.5 to 7.2.6 in /docs
  [#560](https://github.com/strukturag/nextcloud-spreed-signaling/pull/560)
- build(deps): Bump mkdocs from 1.5.2 to 1.5.3 in /docs
  [#561](https://github.com/strukturag/nextcloud-spreed-signaling/pull/561)
- build(deps): Bump markdown from 3.4.4 to 3.5 in /docs
  [#570](https://github.com/strukturag/nextcloud-spreed-signaling/pull/570)
- build(deps): Bump google.golang.org/grpc from 1.58.1 to 1.58.3
  [#573](https://github.com/strukturag/nextcloud-spreed-signaling/pull/573)
- build(deps): Bump github.com/prometheus/client_golang from 1.16.0 to 1.17.0
  [#569](https://github.com/strukturag/nextcloud-spreed-signaling/pull/569)
- build(deps): Bump golang.org/x/net from 0.12.0 to 0.17.0
  [#574](https://github.com/strukturag/nextcloud-spreed-signaling/pull/574)
- build(deps): Bump github.com/nats-io/nats.go from 1.29.0 to 1.30.2
  [#568](https://github.com/strukturag/nextcloud-spreed-signaling/pull/568)
- build(deps): Bump google.golang.org/grpc from 1.58.3 to 1.59.0
  [#578](https://github.com/strukturag/nextcloud-spreed-signaling/pull/578)
- build(deps): Bump github.com/nats-io/nats.go from 1.30.2 to 1.31.0
  [#577](https://github.com/strukturag/nextcloud-spreed-signaling/pull/577)
- dependabot: Check for updates in docker files.
- build(deps): Bump golang from 1.20-alpine to 1.21-alpine in /docker/proxy
  [#581](https://github.com/strukturag/nextcloud-spreed-signaling/pull/581)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.9.22 to 2.10.3
  [#576](https://github.com/strukturag/nextcloud-spreed-signaling/pull/576)
- build(deps): Bump alpine from 3.14 to 3.18 in /docker/janus
  [#582](https://github.com/strukturag/nextcloud-spreed-signaling/pull/582)
- build(deps): Bump golang from 1.20-alpine to 1.21-alpine in /docker/server
  [#583](https://github.com/strukturag/nextcloud-spreed-signaling/pull/583)
- Improve get-version.sh
  [#584](https://github.com/strukturag/nextcloud-spreed-signaling/pull/584)
 -build(deps): Bump go.etcd.io/etcd/client/pkg/v3 from 3.5.9 to 3.5.10
  [#588](https://github.com/strukturag/nextcloud-spreed-signaling/pull/588)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.10.3 to 2.10.4
  [#586](https://github.com/strukturag/nextcloud-spreed-signaling/pull/586)
- build(deps): Bump github.com/google/uuid from 1.3.1 to 1.4.0
  [#585](https://github.com/strukturag/nextcloud-spreed-signaling/pull/585)
- dependabot: Group etcd updates.
- build(deps): Bump the etcd group with 3 updates
  [#590](https://github.com/strukturag/nextcloud-spreed-signaling/pull/590)
- Switch to atomic types from Go 1.19
  [#500](https://github.com/strukturag/nextcloud-spreed-signaling/pull/500)
- Move common flags code to own struct.
  [#591](https://github.com/strukturag/nextcloud-spreed-signaling/pull/591)


## 1.1.3 - 2023-07-05

### Added
- stats: Support configuring subnets for allowed IPs.
  [#448](https://github.com/strukturag/nextcloud-spreed-signaling/pull/448)
- Add common code to handle allowed IPs.
  [#450](https://github.com/strukturag/nextcloud-spreed-signaling/pull/450)
- Add allowall to docker image
  [#488](https://github.com/strukturag/nextcloud-spreed-signaling/pull/488)
- Follow the Go release policy by supporting only the last two versions.
  This drops support for Golang 1.18.
  [#499](https://github.com/strukturag/nextcloud-spreed-signaling/pull/499)

### Changed
- build(deps): Bump google.golang.org/protobuf from 1.29.0 to 1.29.1
  [#446](https://github.com/strukturag/nextcloud-spreed-signaling/pull/446)
- build(deps): Bump actions/setup-go from 3 to 4
  [#447](https://github.com/strukturag/nextcloud-spreed-signaling/pull/447)
- build(deps): Bump google.golang.org/protobuf from 1.29.1 to 1.30.0
  [#449](https://github.com/strukturag/nextcloud-spreed-signaling/pull/449)
- build(deps): Bump coverallsapp/github-action from 1.2.4 to 2.0.0
  [#451](https://github.com/strukturag/nextcloud-spreed-signaling/pull/451)
- build(deps): Bump readthedocs-sphinx-search from 0.2.0 to 0.3.1 in /docs
  [#456](https://github.com/strukturag/nextcloud-spreed-signaling/pull/456)
- build(deps): Bump coverallsapp/github-action from 2.0.0 to 2.1.0
  [#460](https://github.com/strukturag/nextcloud-spreed-signaling/pull/460)
- build(deps): Bump peter-evans/create-or-update-comment from 2 to 3
  [#459](https://github.com/strukturag/nextcloud-spreed-signaling/pull/459)
- build(deps): Bump sphinx from 6.1.3 to 6.2.1 in /docs
  [#468](https://github.com/strukturag/nextcloud-spreed-signaling/pull/468)
- build(deps): Bump mkdocs from 1.4.2 to 1.4.3 in /docs
  [#471](https://github.com/strukturag/nextcloud-spreed-signaling/pull/471)
- build(deps): Bump sphinx-rtd-theme from 1.2.0 to 1.2.1 in /docs
  [#479](https://github.com/strukturag/nextcloud-spreed-signaling/pull/479)
- build(deps): Bump coverallsapp/github-action from 2.1.0 to 2.1.2
  [#466](https://github.com/strukturag/nextcloud-spreed-signaling/pull/466)
- build(deps): Bump golangci/golangci-lint-action from 3.4.0 to 3.5.0
  [#481](https://github.com/strukturag/nextcloud-spreed-signaling/pull/481)
- Simplify vendoring.
  [#482](https://github.com/strukturag/nextcloud-spreed-signaling/pull/482)
- build(deps): Bump sphinx-rtd-theme from 1.2.1 to 1.2.2 in /docs
  [#485](https://github.com/strukturag/nextcloud-spreed-signaling/pull/485)
- build(deps): Bump coverallsapp/github-action from 2.1.2 to 2.2.0
  [#484](https://github.com/strukturag/nextcloud-spreed-signaling/pull/484)
- build(deps): Bump google.golang.org/grpc from 1.53.0 to 1.55.0
  [#472](https://github.com/strukturag/nextcloud-spreed-signaling/pull/472)
- build(deps): Bump go.etcd.io/etcd/client/v3 from 3.5.7 to 3.5.9
  [#473](https://github.com/strukturag/nextcloud-spreed-signaling/pull/473)
- build(deps): Bump github.com/nats-io/nats.go from 1.24.0 to 1.26.0
  [#478](https://github.com/strukturag/nextcloud-spreed-signaling/pull/478)
- build(deps): Bump golangci/golangci-lint-action from 3.5.0 to 3.6.0
  [#492](https://github.com/strukturag/nextcloud-spreed-signaling/pull/492)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.9.15 to 2.9.17
  [#495](https://github.com/strukturag/nextcloud-spreed-signaling/pull/495)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.9.17 to 2.9.18
  [#496](https://github.com/strukturag/nextcloud-spreed-signaling/pull/496)
- build(deps): Bump github.com/prometheus/client_golang from 1.14.0 to 1.15.1
  [#493](https://github.com/strukturag/nextcloud-spreed-signaling/pull/493)
- docker: Don't build concurrently.
  [#498](https://github.com/strukturag/nextcloud-spreed-signaling/pull/498)
- Use "struct{}" channel if only used as signaling mechanism.
  [#491](https://github.com/strukturag/nextcloud-spreed-signaling/pull/491)
- build(deps): Bump google.golang.org/grpc from 1.55.0 to 1.56.0
  [#502](https://github.com/strukturag/nextcloud-spreed-signaling/pull/502)
- build(deps): Bump github.com/prometheus/client_golang from 1.15.1 to 1.16.0
  [#501](https://github.com/strukturag/nextcloud-spreed-signaling/pull/501)
- build(deps): Bump github.com/oschwald/maxminddb-golang from 1.10.0 to 1.11.0
  [#503](https://github.com/strukturag/nextcloud-spreed-signaling/pull/503)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.9.18 to 2.9.19
  [#504](https://github.com/strukturag/nextcloud-spreed-signaling/pull/504)
- build(deps): Bump google.golang.org/grpc from 1.56.0 to 1.56.1
  [#505](https://github.com/strukturag/nextcloud-spreed-signaling/pull/505)
- build(deps): Bump github.com/nats-io/nats.go from 1.27.0 to 1.27.1
  [#506](https://github.com/strukturag/nextcloud-spreed-signaling/pull/506)
- build(deps): Bump google.golang.org/protobuf from 1.30.0 to 1.31.0
  [#507](https://github.com/strukturag/nextcloud-spreed-signaling/pull/507)

### Fixed
- CI: Make sure proxy Docker image is never tagged as "latest".
  [#445](https://github.com/strukturag/nextcloud-spreed-signaling/pull/445)
- Write backends comma-separated to config
  [#487](https://github.com/strukturag/nextcloud-spreed-signaling/pull/487)
- Fix duplicate join events
  [#490](https://github.com/strukturag/nextcloud-spreed-signaling/pull/490)
- Add missing lock for "roomSessionId" to avoid potential races.
  [#497](https://github.com/strukturag/nextcloud-spreed-signaling/pull/497)


## 1.1.2 - 2023-03-13

### Added
- Allow SKIP_VERIFY in docker image.
  [#430](https://github.com/strukturag/nextcloud-spreed-signaling/pull/430)

### Changed
- Keep Docker images alpine based.
  [#427](https://github.com/strukturag/nextcloud-spreed-signaling/pull/427)
- build(deps): Bump coverallsapp/github-action from 1.1.3 to 1.2.0
  [#433](https://github.com/strukturag/nextcloud-spreed-signaling/pull/433)
- build(deps): Bump coverallsapp/github-action from 1.2.0 to 1.2.2
  [#435](https://github.com/strukturag/nextcloud-spreed-signaling/pull/435)
- build(deps): Bump coverallsapp/github-action from 1.2.2 to 1.2.3
  [#436](https://github.com/strukturag/nextcloud-spreed-signaling/pull/436)
- build(deps): Bump coverallsapp/github-action from 1.2.3 to 1.2.4
  [#437](https://github.com/strukturag/nextcloud-spreed-signaling/pull/437)
- build(deps): Bump github.com/nats-io/nats.go from 1.23.0 to 1.24.0
  [#434](https://github.com/strukturag/nextcloud-spreed-signaling/pull/434)
- Run "go mod tidy -compat=1.18".
  [#440](https://github.com/strukturag/nextcloud-spreed-signaling/pull/440)
- CI: Run golangci-lint with Go 1.20
- Update protoc-gen-go-grpc to v1.3.0
  [#442](https://github.com/strukturag/nextcloud-spreed-signaling/pull/442)
- CI: Stop using deprecated "set-output".
  [#441](https://github.com/strukturag/nextcloud-spreed-signaling/pull/441)
- docker: Don't rely on default values when updating TURN settings.
  [#439](https://github.com/strukturag/nextcloud-spreed-signaling/pull/439)
- build(deps): Bump google.golang.org/protobuf from 1.28.1 to 1.29.0
  [#443](https://github.com/strukturag/nextcloud-spreed-signaling/pull/443)

### Fixed
- Fix example in docker README.
  [#429](https://github.com/strukturag/nextcloud-spreed-signaling/pull/429)
- TURN_API_KEY and TURN_SECRET fix.
  [#428](https://github.com/strukturag/nextcloud-spreed-signaling/pull/428)


## 1.1.1 - 2023-02-22

### Fixed
- Fix Docker images.
  [#425](https://github.com/strukturag/nextcloud-spreed-signaling/pull/425)


## 1.1.0 - 2023-02-22

### Added
- Official docker images.
  [#314](https://github.com/strukturag/nextcloud-spreed-signaling/pull/314)
- Use proxy from environment for backend client requests.
  [#326](https://github.com/strukturag/nextcloud-spreed-signaling/pull/326)
- Add aarch64/arm64 docker build
  [#384](https://github.com/strukturag/nextcloud-spreed-signaling/pull/384)
- CI: Setup permissions for workflows.
  [#393](https://github.com/strukturag/nextcloud-spreed-signaling/pull/393)
- Implement "switchto" support
  [#409](https://github.com/strukturag/nextcloud-spreed-signaling/pull/409)
- Allow internal clients to set / change the "inCall" flags.
  [#421](https://github.com/strukturag/nextcloud-spreed-signaling/pull/421)
- Add support for Golang 1.20
  [#413](https://github.com/strukturag/nextcloud-spreed-signaling/pull/413)

### Changed
- Switch to apt-get on CLI.
  [#312](https://github.com/strukturag/nextcloud-spreed-signaling/pull/312)
- vendor: Automatically vendor protobuf modules.
  [#313](https://github.com/strukturag/nextcloud-spreed-signaling/pull/313)
- Bump github.com/prometheus/client_golang from 1.12.2 to 1.13.0
  [#316](https://github.com/strukturag/nextcloud-spreed-signaling/pull/316)
- Bump github.com/oschwald/maxminddb-golang from 1.9.0 to 1.10.0
  [#317](https://github.com/strukturag/nextcloud-spreed-signaling/pull/317)
- Bump github.com/pion/sdp/v3 from 3.0.5 to 3.0.6
  [#320](https://github.com/strukturag/nextcloud-spreed-signaling/pull/320)
- Bump google.golang.org/grpc from 1.48.0 to 1.49.0
  [#324](https://github.com/strukturag/nextcloud-spreed-signaling/pull/324)
- Bump github.com/nats-io/nats-server/v2 from 2.8.4 to 2.9.0
  [#330](https://github.com/strukturag/nextcloud-spreed-signaling/pull/330)
- Bump sphinx from 5.1.1 to 5.2.2 in /docs
  [#339](https://github.com/strukturag/nextcloud-spreed-signaling/pull/339)
- Bump mkdocs from 1.3.1 to 1.4.0 in /docs
  [#340](https://github.com/strukturag/nextcloud-spreed-signaling/pull/340)
- Bump sphinx from 5.2.2 to 5.2.3 in /docs
  [#345](https://github.com/strukturag/nextcloud-spreed-signaling/pull/345)
- Bump github.com/nats-io/nats-server/v2 from 2.9.0 to 2.9.2
  [#344](https://github.com/strukturag/nextcloud-spreed-signaling/pull/344)
- Bump go.etcd.io/etcd/api/v3 from 3.5.4 to 3.5.5
  [#333](https://github.com/strukturag/nextcloud-spreed-signaling/pull/333)
- Bump go.etcd.io/etcd/server/v3 from 3.5.4 to 3.5.5
  [#334](https://github.com/strukturag/nextcloud-spreed-signaling/pull/334)
- Bump google.golang.org/grpc from 1.49.0 to 1.50.0
  [#346](https://github.com/strukturag/nextcloud-spreed-signaling/pull/346)
- Bump github.com/nats-io/nats-server/v2 from 2.9.2 to 2.9.3
  [#348](https://github.com/strukturag/nextcloud-spreed-signaling/pull/348)
- Bump github.com/nats-io/nats.go from 1.17.0 to 1.18.0
  [#349](https://github.com/strukturag/nextcloud-spreed-signaling/pull/349)
- Bump sphinx from 5.2.3 to 5.3.0 in /docs
  [#351](https://github.com/strukturag/nextcloud-spreed-signaling/pull/351)
- Bump mkdocs from 1.4.0 to 1.4.1 in /docs
  [#352](https://github.com/strukturag/nextcloud-spreed-signaling/pull/352)
- Bump google.golang.org/grpc from 1.50.0 to 1.50.1
  [#350](https://github.com/strukturag/nextcloud-spreed-signaling/pull/350)
- Bump golangci/golangci-lint-action from 3.2.0 to 3.3.0
  [#353](https://github.com/strukturag/nextcloud-spreed-signaling/pull/353)
- Bump mkdocs from 1.4.1 to 1.4.2 in /docs
  [#358](https://github.com/strukturag/nextcloud-spreed-signaling/pull/358)
- Bump sphinx-rtd-theme from 1.0.0 to 1.1.0 in /docs
  [#357](https://github.com/strukturag/nextcloud-spreed-signaling/pull/357)
- Bump github.com/nats-io/nats.go from 1.18.0 to 1.19.0
  [#354](https://github.com/strukturag/nextcloud-spreed-signaling/pull/354)
- Bump github.com/prometheus/client_golang from 1.13.0 to 1.13.1
  [#360](https://github.com/strukturag/nextcloud-spreed-signaling/pull/360)
- Bump github.com/nats-io/nats-server/v2 from 2.9.3 to 2.9.5
  [#359](https://github.com/strukturag/nextcloud-spreed-signaling/pull/359)
- build(deps): Bump golangci/golangci-lint-action from 3.3.0 to 3.3.1
  [#365](https://github.com/strukturag/nextcloud-spreed-signaling/pull/365)
- build(deps): Bump sphinx-rtd-theme from 1.1.0 to 1.1.1 in /docs
  [#363](https://github.com/strukturag/nextcloud-spreed-signaling/pull/363)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.9.5 to 2.9.6
  [#361](https://github.com/strukturag/nextcloud-spreed-signaling/pull/361)
- build(deps): Bump github.com/nats-io/nats.go from 1.19.0 to 1.20.0
  [#366](https://github.com/strukturag/nextcloud-spreed-signaling/pull/366)
- build(deps): Bump google.golang.org/grpc from 1.50.1 to 1.51.0
  [#368](https://github.com/strukturag/nextcloud-spreed-signaling/pull/368)
- build(deps): Bump github.com/prometheus/client_golang from 1.13.1 to 1.14.0
  [#364](https://github.com/strukturag/nextcloud-spreed-signaling/pull/364)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.9.6 to 2.9.7
  [#367](https://github.com/strukturag/nextcloud-spreed-signaling/pull/367)
- build(deps): Bump go.etcd.io/etcd/server/v3 from 3.5.5 to 3.5.6
  [#372](https://github.com/strukturag/nextcloud-spreed-signaling/pull/372)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.9.7 to 2.9.8
  [#371](https://github.com/strukturag/nextcloud-spreed-signaling/pull/371)
- build(deps): Bump github.com/nats-io/nats.go from 1.20.0 to 1.21.0
  [#375](https://github.com/strukturag/nextcloud-spreed-signaling/pull/375)
- build(deps): Bump github.com/golang-jwt/jwt/v4 from 4.4.2 to 4.4.3
  [#374](https://github.com/strukturag/nextcloud-spreed-signaling/pull/374)
- build(deps): Bump cirrus-actions/rebase from 1.7 to 1.8
  [#379](https://github.com/strukturag/nextcloud-spreed-signaling/pull/379)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.9.8 to 2.9.9
  [#377](https://github.com/strukturag/nextcloud-spreed-signaling/pull/377)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.9.9 to 2.9.10
  [#382](https://github.com/strukturag/nextcloud-spreed-signaling/pull/382)
- build(deps): Bump github.com/nats-io/nats.go from 1.21.0 to 1.22.1
  [#383](https://github.com/strukturag/nextcloud-spreed-signaling/pull/383)
- build(deps): Bump google.golang.org/grpc from 1.51.0 to 1.52.0
  [#391](https://github.com/strukturag/nextcloud-spreed-signaling/pull/391)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.9.10 to 2.9.11
  [#387](https://github.com/strukturag/nextcloud-spreed-signaling/pull/387)
- Stop using WaitGroup to detect finished message processing.
  [#394](https://github.com/strukturag/nextcloud-spreed-signaling/pull/394)
- Improve handling of throttled responses from Nextcloud.
  [#395](https://github.com/strukturag/nextcloud-spreed-signaling/pull/395)
- Test: add timeout while waiting for etcd event.
  [#397](https://github.com/strukturag/nextcloud-spreed-signaling/pull/397)
- build(deps): Bump github.com/nats-io/nats.go from 1.22.1 to 1.23.0
  [#399](https://github.com/strukturag/nextcloud-spreed-signaling/pull/399)
- build(deps): Bump go.etcd.io/etcd/api/v3 from 3.5.6 to 3.5.7
  [#402](https://github.com/strukturag/nextcloud-spreed-signaling/pull/402)
- build(deps): Bump go.etcd.io/etcd/client/v3 from 3.5.6 to 3.5.7
  [#403](https://github.com/strukturag/nextcloud-spreed-signaling/pull/403)
- build(deps): Bump go.etcd.io/etcd/server/v3 from 3.5.6 to 3.5.7
  [#404](https://github.com/strukturag/nextcloud-spreed-signaling/pull/404)
- build(deps): Bump golangci/golangci-lint-action from 3.3.1 to 3.4.0
  [#405](https://github.com/strukturag/nextcloud-spreed-signaling/pull/405)
- build(deps): Bump readthedocs-sphinx-search from 0.1.2 to 0.2.0 in /docs
  [#407](https://github.com/strukturag/nextcloud-spreed-signaling/pull/407)
- build(deps): Bump google.golang.org/grpc from 1.52.0 to 1.52.1
  [#406](https://github.com/strukturag/nextcloud-spreed-signaling/pull/406)
- build(deps): Bump docker/build-push-action from 3 to 4
  [#412](https://github.com/strukturag/nextcloud-spreed-signaling/pull/412)
- build(deps): Bump google.golang.org/grpc from 1.52.1 to 1.52.3
  [#410](https://github.com/strukturag/nextcloud-spreed-signaling/pull/410)
- Explicitly use type "sysConn".
  [#416](https://github.com/strukturag/nextcloud-spreed-signaling/pull/416)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.9.11 to 2.9.14
  [#415](https://github.com/strukturag/nextcloud-spreed-signaling/pull/415)
- build(deps): Bump sphinx-rtd-theme from 1.1.1 to 1.2.0 in /docs
  [#418](https://github.com/strukturag/nextcloud-spreed-signaling/pull/418)
- build(deps): Bump google.golang.org/grpc from 1.52.3 to 1.53.0
  [#417](https://github.com/strukturag/nextcloud-spreed-signaling/pull/417)
- build(deps): Bump golang.org/x/net from 0.5.0 to 0.7.0
  [#422](https://github.com/strukturag/nextcloud-spreed-signaling/pull/422)
- build(deps): Bump github.com/golang-jwt/jwt/v4 from 4.4.3 to 4.5.0
  [#423](https://github.com/strukturag/nextcloud-spreed-signaling/pull/423)
- build(deps): Bump sphinx from 5.3.0 to 6.1.3 in /docs
  [#390](https://github.com/strukturag/nextcloud-spreed-signaling/pull/390)
- Various refactorings to simplify code
  [#400](https://github.com/strukturag/nextcloud-spreed-signaling/pull/400)

### Fixed
- Remove @resources from SystemCallFilter
  [#322](https://github.com/strukturag/nextcloud-spreed-signaling/pull/322)
- Fix deadlock for proxy connection issues
  [#327](https://github.com/strukturag/nextcloud-spreed-signaling/pull/327)
- Fix goroutines leak check.
  [#396](https://github.com/strukturag/nextcloud-spreed-signaling/pull/396)


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
