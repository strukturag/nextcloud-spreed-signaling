# Changelog

All notable changes to this project will be documented in this file.

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
