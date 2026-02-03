# Changelog

All notable changes to this project will be documented in this file.

## 2.1.0 - 2026-02-03

### Added
- Introduce "internal" package
  [#1082](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1082)
- Add etcd TLS tests.
  [#1084](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1084)
- Add missing stats registration.
  [#1086](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1086)
- Add commands to the readme on how to build Docker images locally.
  [#1088](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1088)
- Use gvisor checklocks for static lock analysis.
  [#1078](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1078)
- Support relaying of chat messages.
  [#868](https://github.com/strukturag/nextcloud-spreed-signaling/pull/868)
- Return bandwidth information in room responses.
  [#1099](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1099)
- Expose real bandwidth usage through metrics.
  [#1102](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1102)
- Add type to store bandwidths.
  [#1108](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1108)
- Add more WebRTC-related metrics
  [#1109](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1109)
- Add metrics about client bytes/messages sent/received.
  [#1134](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1134)
- Introduce transient session data.
  [#1120](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1120)
- Include "version.txt" in tarball.
  [#1142](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1142)
- CI: Also upload images to quay.io/strukturag/nextcloud-spreed-signaling
  [#1159](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1159)
- Add more metrics about sessions in calls.
  [#1183](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1183)

### Changed
- dockerfile: create system user instead of normal user, avoid home directory
  [#1058](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1058)
- Use "testing/synctest" to simplify timing-dependent tests.
  [#1067](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1067)
- Add dedicated types for different session ids.
  [#1066](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1066)
- Move "StringMap" class to api module.
  [#1077](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1077)
- CI: Disable "stdversion" check of govet.
  [#1079](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1079)
- CI: Use codecov components.
  [#1080](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1080)
- Add interface for method "GetInCall".
  [#1083](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1083)
- Make LruCache typed through generics.
  [#1085](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1085)
- Protect access to the debug pprof handlers.
  [#1094](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1094)
- Don't use environment to keep per-test properties.
  [#1106](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1106)
- Add formatting to bandwidth values.
  [#1114](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1114)
- Don't format zero bandwidth as "unlimited".
  [#1115](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1115)
- CI: Split test jobs to speed up total actions time.
  [#1118](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1118)
- Stop using global logger
  [#1117](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1117)
- CI: Split tarball jobs to speed up total actions time.
  [#1129](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1129)
- Update client code
  [#1130](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1130)
- Don't use fmt.Sprintf where not necessary.
  [#1131](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1131)
- Use test-related logger for embedded etcd.
  [#1132](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1132)
- No need to use list of pointers, use objects directly.
  [#1133](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1133)
- Generate shorter session ids.
  [#1140](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1140)
- client: Include version, optimize JSON processing.
  [#1143](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1143)
- Enable more linters
  [#1145](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1145)
- Parallelize more tests.
  [#1149](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1149)
- Move logging code to separate package.
  [#1150](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1150)
- Close subscriber synchronously on errors.
  [#1152](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1152)
- CI: Run "modernize" with Go 1.25
  [#1160](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1160)
- CI: Always use latest patch release for govuln checks.
  [#1161](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1161)
- Process all NATS messages for same target from single goroutine.
  [#1165](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1165)
- CI: Process files in all folders with licensecheck.
  [#1169](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1169)
- CI: Run checklocks with Go 1.25
  [#1170](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1170)
- Refactor code into packages
  [#1151](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1151)
- Remove unused testing code.
  [#1174](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1174)
- checklocks: Remove ignore since generics are supported now.
  [#1184](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1184)
- Support receiving and forwarding multiple chat messages from Talk.
  [#1185](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1185)
- Move tests closer to code being checked
  [#1186](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1186)

### Fixed
- A proxy connection is only connected after a hello has been processed.
  [#1071](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1071)
- Fix URL to send federated ping requests.
  [#1081](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1081)
- Reconnect proxy connection even if shutdown was scheduled before.
  [#1100](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1100)
- Federation cleanup fixes.
  [#1105](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1105)
- Also rewrite token in comment for federated chat relay.
  [#1112](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1112)
- Fix transient data for clustered setups.
  [#1121](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1121)
- Fix initial transient data in clustered setups
  [#1127](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1127)
- fix(docs): already_joined error response
  [#1126](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1126)
- Fix storing initial data when clustered.
  [#1128](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1128)
- Fix flaky tests that fail under load.
  [#1153](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1153)

### Dependencies
- Bump google.golang.org/grpc from 1.74.2 to 1.75.0
  [#1054](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1054)
- Bump github.com/nats-io/nats.go from 1.44.0 to 1.45.0
  [#1057](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1057)
- Bump google.golang.org/protobuf from 1.36.7 to 1.36.8
  [#1056](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1056)
- Bump github.com/stretchr/testify from 1.10.0 to 1.11.0
  [#1059](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1059)
- Bump github.com/stretchr/testify from 1.11.0 to 1.11.1
  [#1060](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1060)
- Bump markdown from 3.8.2 to 3.9 in /docs
  [#1065](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1065)
- Bump github.com/pion/sdp/v3 from 3.0.15 to 3.0.16
  [#1061](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1061)
- Bump github.com/prometheus/client_golang from 1.23.0 to 1.23.2
  [#1064](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1064)
- Bump actions/setup-go from 5 to 6
  [#1062](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1062)
- Bump google.golang.org/grpc from 1.75.0 to 1.75.1
  [#1070](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1070)
- Bump google.golang.org/protobuf from 1.36.8 to 1.36.9
  [#1069](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1069)
- Bump github.com/nats-io/nats-server/v2 from 2.11.8 to 2.11.9
  [#1068](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1068)
- Bump github.com/mailru/easyjson from 0.9.0 to 0.9.1
  [#1072](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1072)
- Bump the etcd group with 4 updates
  [#1073](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1073)
- Bump github.com/nats-io/nats.go from 1.45.0 to 1.46.0
  [#1076](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1076)
- Bump github.com/nats-io/nats-server/v2 from 2.11.9 to 2.12.0
  [#1075](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1075)
- Bump github.com/nats-io/nats.go from 1.46.0 to 1.46.1
  [#1087](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1087)
- Bump google.golang.org/grpc from 1.75.1 to 1.76.0
  [#1092](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1092)
- Bump github/codeql-action from 3 to 4
  [#1093](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1093)
- Bump peter-evans/create-or-update-comment from 4 to 5
  [#1090](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1090)
- Bump google.golang.org/protobuf from 1.36.9 to 1.36.10
  [#1089](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1089)
- Bump github.com/nats-io/nats.go from 1.46.1 to 1.47.0
  [#1096](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1096)
- Bump github.com/nats-io/nats-server/v2 from 2.12.0 to 2.12.1
  [#1095](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1095)
- Bump the artifacts group with 2 updates
  [#1101](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1101)
- Bump markdown from 3.9 to 3.10 in /docs
  [#1104](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1104)
- Bump golangci/golangci-lint-action from 8.0.0 to 9.0.0
  [#1110](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1110)
- Bump the etcd group with 4 updates
  [#1111](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1111)
- Bump github.com/nats-io/nats-server/v2 from 2.12.1 to 2.12.2
  [#1113](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1113)
- Bump google.golang.org/grpc from 1.76.0 to 1.77.0
  [#1116](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1116)
- Bump golang.org/x/crypto from 0.43.0 to 0.45.0
  [#1119](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1119)
- Bump go.uber.org/zap from 1.27.0 to 1.27.1
  [#1123](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1123)
- Bump actions/checkout from 5 to 6
  [#1124](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1124)
- Bump golangci/golangci-lint-action from 9.0.0 to 9.1.0
  [#1125](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1125)
- Bump github.com/pion/ice/v4 from 4.0.10 to 4.0.11
  [#1135](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1135)
- Bump google.golang.org/grpc/cmd/protoc-gen-go-grpc from 1.5.1 to 1.6.0
  [#1136](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1136)
- Bump github.com/pion/ice/v4 from 4.0.11 to 4.0.12
  [#1138](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1138)
- Bump golangci/golangci-lint-action from 9.1.0 to 9.2.0
  [#1144](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1144)
- Bump github.com/pion/ice/v4 from 4.0.12 to 4.0.13
  [#1146](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1146)
- Bump actions/cache from 4 to 5
  [#1157](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1157)
- Bump google.golang.org/protobuf from 1.36.10 to 1.36.11
  [#1156](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1156)
- Bump github.com/pion/ice/v4 from 4.0.13 to 4.1.0
  [#1155](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1155)
- Bump the artifacts group with 2 updates
  [#1154](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1154)
- Bump github.com/nats-io/nats.go from 1.47.0 to 1.48.0
  [#1163](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1163)
- Bump the etcd group with 4 updates
  [#1162](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1162)
- Bump github.com/nats-io/nats-server/v2 from 2.12.2 to 2.12.3
  [#1164](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1164)
- Bump github.com/pion/sdp/v3 from 3.0.16 to 3.0.17
  [#1166](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1166)
- Bump google.golang.org/grpc from 1.77.0 to 1.78.0
  [#1167](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1167)
- Bump github.com/pion/ice/v4 from 4.1.0 to 4.2.0
  [#1171](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1171)
- Bump sphinx-rtd-theme from 3.0.2 to 3.1.0 in /docs
  [#1172](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1172)
- Bump sphinx from 8.2.3 to 9.1.0 in /docs
  [#1168](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1168)
- Bump markdown from 3.10 to 3.10.1 in /docs
  [#1177](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1177)
- Bump github.com/nats-io/nats-server/v2 from 2.12.3 to 2.12.4
  [#1179](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1179)
- Bump github.com/golang-jwt/jwt/v5 from 5.3.0 to 5.3.1
  [#1182](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1182)


## 2.0.4 - 2025-08-18

### Added
- Comment / document possible error responses.
  [#1004](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1004)
- Support multiple sessions for dialout.
  [#1005](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1005)
- Support filtering candidates received by clients.
  [#1000](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1000)
- Describe how to pass caller information for outgoing calls.
  [#1019](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1019)
- Support multiple urls per backend
  [#770](https://github.com/strukturag/nextcloud-spreed-signaling/pull/770)
- Return connection / publisher tokens for remote publishers.
  [#1025](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1025)

### Changed
- Drop support for Go 1.23
  [#1049](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1049)
- Only forward actor id / -type in "addsession" request if both are given.
  [#1009](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1009)
- Use backend id in backend client stats to match other stats.
  [#1020](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1020)
- Remove debug output.
  [#1022](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1022)
- Only forward actor details in leave virtual sessions request if both are given.
  [#1026](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1026)
- Delete (unused) proxy publisher/subscriber created after local timeout.
  [#1032](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1032)
- modernize: Replace "interface{}" with "any".
  [#1033](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1033)
- Add type for string maps.
  [#1034](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1034)
- CI: Migrate to codecov.
  [#1037](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1037)
  [#1038](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1038)
- Use testify assertions to check expected fields / values internally.
  [#1035](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1035)
- CI: Test with Golang 1.25
  [#1048](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1048)
- Modernize Go code and check from CI.
  [#1050](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1050)
- Test "HasAnyPermission" method.
  [#1051](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1051)
- Use standard library where possible.
  [#1052](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1052)

### Fixed
- Fix deadlock when setting transient data while removing listener.
  [#992](https://github.com/strukturag/nextcloud-spreed-signaling/pull/992)
- Fixes for file watcher special cases
  [#1017](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1017)
- Fix updating metric "signaling_mcu_subscribers" in various error cases.
  [#1027](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1027)

### Dependencies
- Bump google.golang.org/grpc from 1.72.0 to 1.72.1
  [#989](https://github.com/strukturag/nextcloud-spreed-signaling/pull/989)
- Bump github.com/pion/sdp/v3 from 3.0.11 to 3.0.12
  [#991](https://github.com/strukturag/nextcloud-spreed-signaling/pull/991)
- Bump the etcd group with 4 updates
  [#990](https://github.com/strukturag/nextcloud-spreed-signaling/pull/990)
- Bump github.com/nats-io/nats-server/v2 from 2.11.3 to 2.11.4
  [#993](https://github.com/strukturag/nextcloud-spreed-signaling/pull/993)
- Bump github.com/pion/sdp/v3 from 3.0.12 to 3.0.13
  [#994](https://github.com/strukturag/nextcloud-spreed-signaling/pull/994)
- Bump google.golang.org/grpc from 1.72.1 to 1.72.2
  [#995](https://github.com/strukturag/nextcloud-spreed-signaling/pull/995)
- Bump github.com/nats-io/nats.go from 1.42.0 to 1.43.0
  [#999](https://github.com/strukturag/nextcloud-spreed-signaling/pull/999)
- Bump google.golang.org/grpc from 1.72.2 to 1.73.0
  [#1001](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1001)
- Bump the etcd group with 4 updates
  [#1002](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1002)
- Bump markdown from 3.8 to 3.8.2 in /docs
  [#1008](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1008)
- Bump github.com/pion/sdp/v3 from 3.0.13 to 3.0.14
  [#1006](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1006)
- Bump github.com/nats-io/nats-server/v2 from 2.11.4 to 2.11.5
  [#1010](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1010)
- Bump github.com/nats-io/nats-server/v2 from 2.11.5 to 2.11.6
  [#1011](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1011)
- Bump the etcd group with 4 updates
  [#1015](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1015)
- Bump github.com/golang-jwt/jwt/v5 from 5.2.2 to 5.2.3
  [#1016](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1016)
- Bump google.golang.org/grpc from 1.73.0 to 1.74.0
  [#1018](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1018)
- Bump github.com/pion/sdp/v3 from 3.0.14 to 3.0.15
  [#1021](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1021)
- Bump the etcd group with 4 updates
  [#1023](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1023)
- Bump google.golang.org/grpc from 1.74.0 to 1.74.2
  [#1024](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1024)
- Bump the etcd group with 4 updates
  [#1028](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1028)
- Bump github.com/nats-io/nats.go from 1.43.0 to 1.44.0
  [#1031](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1031)
- Bump github.com/golang-jwt/jwt/v5 from 5.2.3 to 5.3.0
  [#1036](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1036)
- Bump github.com/prometheus/client_golang from 1.22.0 to 1.23.0
  [#1039](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1039)
- Bump github.com/nats-io/nats-server/v2 from 2.11.6 to 2.11.7
  [#1040](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1040)
- Bump google.golang.org/protobuf from 1.36.6 to 1.36.7
  [#1043](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1043)
- Bump actions/checkout from 4 to 5
  [#1044](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1044)
- Bump actions/download-artifact from 4 to 5 in the artifacts group
  [#1042](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1042)
- Bump golang from 1.24-alpine to 1.25-alpine in /docker/server
  [#1047](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1047)
- Bump golang from 1.24-alpine to 1.25-alpine in /docker/proxy
  [#1046](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1046)
- Bump github.com/nats-io/nats-server/v2 from 2.11.7 to 2.11.8
  [#1053](https://github.com/strukturag/nextcloud-spreed-signaling/pull/1053)


## 2.0.3 - 2025-05-07

### Added
- Allow using environment variables for sessions and clients secrets
  [#910](https://github.com/strukturag/nextcloud-spreed-signaling/pull/910)
- Allow using environment variables for backend secrets
  [#912](https://github.com/strukturag/nextcloud-spreed-signaling/pull/912)
- Add serverinfo API
  [#937](https://github.com/strukturag/nextcloud-spreed-signaling/pull/937)
- Add metrics for backend client requests.
  [#973](https://github.com/strukturag/nextcloud-spreed-signaling/pull/973)

### Changed
- Drop support for Go 1.22
  [#969](https://github.com/strukturag/nextcloud-spreed-signaling/pull/969)
- Do not log nats url credentials
  [#911](https://github.com/strukturag/nextcloud-spreed-signaling/pull/911)
- Migrate cache-control parsing to https://github.com/pquerna/cachecontrol
  [#916](https://github.com/strukturag/nextcloud-spreed-signaling/pull/916)
- CI: Test with Golang 1.24
  [#922](https://github.com/strukturag/nextcloud-spreed-signaling/pull/922)
- Add "/usr/lib64" to systemd ExecPath
  [#963](https://github.com/strukturag/nextcloud-spreed-signaling/pull/963)
- Improve memory allocations
  [#870](https://github.com/strukturag/nextcloud-spreed-signaling/pull/870)
- Speedup tests
  [#972](https://github.com/strukturag/nextcloud-spreed-signaling/pull/972)
- docker: Make more settings configurable
  [#980](https://github.com/strukturag/nextcloud-spreed-signaling/pull/980)
- Add jitter to reconnect intervals.
  [#988](https://github.com/strukturag/nextcloud-spreed-signaling/pull/988)

### Fixed
- nats: Reconnect client indefinitely.
  [#935](https://github.com/strukturag/nextcloud-spreed-signaling/pull/935)
- Explicitly set TMPDIR to ensure that it is an executable path
  [#956](https://github.com/strukturag/nextcloud-spreed-signaling/pull/956)
- Close subscribers on errors during initial connection.
  [#959](https://github.com/strukturag/nextcloud-spreed-signaling/pull/959)
- Fix formatting of errors in "assert.Fail" calls.
  [#970](https://github.com/strukturag/nextcloud-spreed-signaling/pull/970)
- Fix race condition in flaky certificate/CA reload tests.
  [#971](https://github.com/strukturag/nextcloud-spreed-signaling/pull/971)
- Fix flaky test "Test_GrpcClients_DnsDiscovery".
  [#976](https://github.com/strukturag/nextcloud-spreed-signaling/pull/976)
- Fix subscribers not closed when publisher is closed in Janus 1.x
  [#986](https://github.com/strukturag/nextcloud-spreed-signaling/pull/986)
- Close subscriber if remote publisher was closed.
  [#987](https://github.com/strukturag/nextcloud-spreed-signaling/pull/987)

### Dependencies
- Bump google.golang.org/grpc from 1.69.4 to 1.70.0
  [#904](https://github.com/strukturag/nextcloud-spreed-signaling/pull/904)
- Bump the etcd group with 4 updates
  [#907](https://github.com/strukturag/nextcloud-spreed-signaling/pull/907)
- Bump coverallsapp/github-action from 2.3.4 to 2.3.6
  [#909](https://github.com/strukturag/nextcloud-spreed-signaling/pull/909)
- Bump google.golang.org/protobuf from 1.36.3 to 1.36.4
  [#908](https://github.com/strukturag/nextcloud-spreed-signaling/pull/908)
- Bump github.com/nats-io/nats-server/v2 from 2.10.24 to 2.10.25
  [#903](https://github.com/strukturag/nextcloud-spreed-signaling/pull/903)
- Bump golangci/golangci-lint-action from 6.2.0 to 6.3.0
  [#913](https://github.com/strukturag/nextcloud-spreed-signaling/pull/913)
- Bump github.com/nats-io/nats.go from 1.38.0 to 1.39.0
  [#915](https://github.com/strukturag/nextcloud-spreed-signaling/pull/915)
- Bump golangci/golangci-lint-action from 6.3.0 to 6.3.2
  [#918](https://github.com/strukturag/nextcloud-spreed-signaling/pull/918)
- Bump google.golang.org/protobuf from 1.36.4 to 1.36.5
  [#917](https://github.com/strukturag/nextcloud-spreed-signaling/pull/917)
- build(deps): bump golang from 1.23-alpine to 1.24-alpine in /docker/proxy
  [#921](https://github.com/strukturag/nextcloud-spreed-signaling/pull/921)
- build(deps): bump golang from 1.23-alpine to 1.24-alpine in /docker/server
  [#919](https://github.com/strukturag/nextcloud-spreed-signaling/pull/919)
- build(deps): bump sphinx from 8.1.3 to 8.2.0 in /docs
  [#928](https://github.com/strukturag/nextcloud-spreed-signaling/pull/928)
- build(deps): bump github.com/prometheus/client_golang from 1.20.5 to 1.21.0
  [#927](https://github.com/strukturag/nextcloud-spreed-signaling/pull/927)
- build(deps): bump sphinx from 8.2.0 to 8.2.1 in /docs
  [#929](https://github.com/strukturag/nextcloud-spreed-signaling/pull/929)
- build(deps): bump github.com/nats-io/nats.go from 1.39.0 to 1.39.1
  [#926](https://github.com/strukturag/nextcloud-spreed-signaling/pull/926)
- build(deps): bump sphinx from 8.2.1 to 8.2.3 in /docs
  [#932](https://github.com/strukturag/nextcloud-spreed-signaling/pull/932)
- build(deps): bump google.golang.org/grpc from 1.70.0 to 1.71.0
  [#933](https://github.com/strukturag/nextcloud-spreed-signaling/pull/933)
- build(deps): bump golangci/golangci-lint-action from 6.3.3 to 6.5.0
  [#925](https://github.com/strukturag/nextcloud-spreed-signaling/pull/925)
- build(deps): bump jinja2 from 3.1.5 to 3.1.6 in /docs
  [#938](https://github.com/strukturag/nextcloud-spreed-signaling/pull/938)
- build(deps): bump github.com/pion/sdp/v3 from 3.0.10 to 3.0.11
  [#939](https://github.com/strukturag/nextcloud-spreed-signaling/pull/939)
- build(deps): bump golangci/golangci-lint-action from 6.5.0 to 6.5.1
  [#940](https://github.com/strukturag/nextcloud-spreed-signaling/pull/940)
- build(deps): bump golangci/golangci-lint-action from 6.5.1 to 6.5.2
  [#946](https://github.com/strukturag/nextcloud-spreed-signaling/pull/946)
- build(deps): bump github.com/golang-jwt/jwt/v5 from 5.2.1 to 5.2.2
  [#948](https://github.com/strukturag/nextcloud-spreed-signaling/pull/948)
- build(deps): bump github.com/golang-jwt/jwt/v4 from 4.5.1 to 4.5.2
  [#949](https://github.com/strukturag/nextcloud-spreed-signaling/pull/949)
- build(deps): bump golangci/golangci-lint-action from 6.5.2 to 7.0.0
  [#951](https://github.com/strukturag/nextcloud-spreed-signaling/pull/951)
- build(deps): bump google.golang.org/protobuf from 1.36.5 to 1.36.6
  [#952](https://github.com/strukturag/nextcloud-spreed-signaling/pull/952)
- Bump markdown from 3.7 to 3.8 in /docs
  [#966](https://github.com/strukturag/nextcloud-spreed-signaling/pull/966)
- Bump golang.org/x/crypto from 0.32.0 to 0.35.0
  [#967](https://github.com/strukturag/nextcloud-spreed-signaling/pull/967)
- Bump github.com/nats-io/nats.go from 1.39.1 to 1.41.1
  [#964](https://github.com/strukturag/nextcloud-spreed-signaling/pull/964)
- Bump google.golang.org/grpc from 1.71.0 to 1.71.1
  [#957](https://github.com/strukturag/nextcloud-spreed-signaling/pull/957)
- build(deps): bump golang.org/x/net from 0.34.0 to 0.36.0
  [#941](https://github.com/strukturag/nextcloud-spreed-signaling/pull/941)
- build(deps): bump the etcd group with 4 updates
  [#936](https://github.com/strukturag/nextcloud-spreed-signaling/pull/936)
- Bump github.com/nats-io/nats-server/v2 from 2.10.25 to 2.11.1
  [#962](https://github.com/strukturag/nextcloud-spreed-signaling/pull/962)
- Bump github.com/prometheus/client_golang from 1.21.1 to 1.22.0
  [#974](https://github.com/strukturag/nextcloud-spreed-signaling/pull/974)
- Bump github.com/fsnotify/fsnotify from 1.8.0 to 1.9.0
  [#975](https://github.com/strukturag/nextcloud-spreed-signaling/pull/975)
- Bump google.golang.org/grpc from 1.71.1 to 1.72.0
  [#978](https://github.com/strukturag/nextcloud-spreed-signaling/pull/978)
- Bump github.com/nats-io/nats.go from 1.41.1 to 1.41.2
  [#977](https://github.com/strukturag/nextcloud-spreed-signaling/pull/977)
- Bump github.com/nats-io/nats-server/v2 from 2.11.1 to 2.11.3
  [#982](https://github.com/strukturag/nextcloud-spreed-signaling/pull/982)
- Bump github.com/nats-io/nats.go from 1.41.2 to 1.42.0
  [#983](https://github.com/strukturag/nextcloud-spreed-signaling/pull/983)
- Bump golangci/golangci-lint-action from 7.0.0 to 8.0.0
  [#985](https://github.com/strukturag/nextcloud-spreed-signaling/pull/985)


## 2.0.2 - 2025-01-22

### Added
- Support passing codec parameters when creating publishers.
  [#853](https://github.com/strukturag/nextcloud-spreed-signaling/pull/853)
- Support recipient "call".
  [#859](https://github.com/strukturag/nextcloud-spreed-signaling/pull/859)
- Include client features in "join" events.
  [#879](https://github.com/strukturag/nextcloud-spreed-signaling/pull/879)
- Include features of federated clients in "join" events.
  [#882](https://github.com/strukturag/nextcloud-spreed-signaling/pull/882)
- Check version of cluster nodes and log warning if different.
  [#898](https://github.com/strukturag/nextcloud-spreed-signaling/pull/898)
- Add feature id for supporting codec parameters in offer.
  [#902](https://github.com/strukturag/nextcloud-spreed-signaling/pull/902)

### Changed
- Drop support for Go 1.21
  [#858](https://github.com/strukturag/nextcloud-spreed-signaling/pull/858)
- Don't redefine built-in id "max".
  [#863](https://github.com/strukturag/nextcloud-spreed-signaling/pull/863)
- Notify remote to stop publishing when last local subscriber is closed.
  [#860](https://github.com/strukturag/nextcloud-spreed-signaling/pull/860)
- Add testcase when joining unknown room.
  [#869](https://github.com/strukturag/nextcloud-spreed-signaling/pull/869)
- docker: apply Docker image versioning
  [#873](https://github.com/strukturag/nextcloud-spreed-signaling/pull/873)
- docker: Use bind-mount for "gnatsd.conf"
  [#881](https://github.com/strukturag/nextcloud-spreed-signaling/pull/881)
- docker: Upgrade Janus and its dependencies
  [#874](https://github.com/strukturag/nextcloud-spreed-signaling/pull/874)
- make: Pin version of "google.golang.org/protobuf/cmd/protoc-gen-go".
  [#897](https://github.com/strukturag/nextcloud-spreed-signaling/pull/897)
- make: Optimize generated easyjson files.
  [#899](https://github.com/strukturag/nextcloud-spreed-signaling/pull/899)

### Fixed
- Prevent duplicate virtual sessions in participant update events.
  [#851](https://github.com/strukturag/nextcloud-spreed-signaling/pull/851)
- proxy: Close client connection if session is expired / closed.
  [#852](https://github.com/strukturag/nextcloud-spreed-signaling/pull/852)

### Dependencies
- Bump github.com/fsnotify/fsnotify from 1.7.0 to 1.8.0
  [#854](https://github.com/strukturag/nextcloud-spreed-signaling/pull/854)
- Bump github.com/golang-jwt/jwt/v4 from 4.5.0 to 4.5.1
  [#856](https://github.com/strukturag/nextcloud-spreed-signaling/pull/856)
- Migrate to github.com/golang-jwt/jwt/v5
  [#857](https://github.com/strukturag/nextcloud-spreed-signaling/pull/857)
- Bump the etcd group with 4 updates
  [#817](https://github.com/strukturag/nextcloud-spreed-signaling/pull/817)
- Bump sphinx-rtd-theme from 3.0.1 to 3.0.2 in /docs
  [#865](https://github.com/strukturag/nextcloud-spreed-signaling/pull/865)
- Bump the etcd group with 4 updates
  [#864](https://github.com/strukturag/nextcloud-spreed-signaling/pull/864)
- Bump google.golang.org/protobuf from 1.35.1 to 1.35.2
  [#866](https://github.com/strukturag/nextcloud-spreed-signaling/pull/866)
- Bump github.com/stretchr/testify from 1.9.0 to 1.10.0
  [#872](https://github.com/strukturag/nextcloud-spreed-signaling/pull/872)
- Bump github.com/nats-io/nats-server/v2 from 2.10.22 to 2.10.23
  [#878](https://github.com/strukturag/nextcloud-spreed-signaling/pull/878)
- Bump alpine from 3.20 to 3.21 in /docker/janus
  [#876](https://github.com/strukturag/nextcloud-spreed-signaling/pull/876)
- Bump golang.org/x/crypto from 0.30.0 to 0.31.0
  [#880](https://github.com/strukturag/nextcloud-spreed-signaling/pull/880)
- Bump google.golang.org/grpc from 1.67.1 to 1.68.1
  [#875](https://github.com/strukturag/nextcloud-spreed-signaling/pull/875)
- Bump google.golang.org/protobuf from 1.35.2 to 1.36.0
  [#884](https://github.com/strukturag/nextcloud-spreed-signaling/pull/884)
- Bump github.com/mailru/easyjson from 0.7.7 to 0.9.0
  [#885](https://github.com/strukturag/nextcloud-spreed-signaling/pull/885)
- Bump github.com/nats-io/nats.go from 1.37.0 to 1.38.0
  [#887](https://github.com/strukturag/nextcloud-spreed-signaling/pull/887)
- Bump google.golang.org/grpc from 1.68.1 to 1.69.2
  [#888](https://github.com/strukturag/nextcloud-spreed-signaling/pull/888)
- Bump github.com/nats-io/nats-server/v2 from 2.10.23 to 2.10.24
  [#886](https://github.com/strukturag/nextcloud-spreed-signaling/pull/886)
- Bump jinja2 from 3.1.4 to 3.1.5 in /docs
  [#889](https://github.com/strukturag/nextcloud-spreed-signaling/pull/889)
- Bump google.golang.org/protobuf from 1.36.0 to 1.36.1
  [#890](https://github.com/strukturag/nextcloud-spreed-signaling/pull/890)
- Bump google.golang.org/grpc from 1.69.2 to 1.69.4
  [#894](https://github.com/strukturag/nextcloud-spreed-signaling/pull/894)
- Bump github.com/pion/sdp/v3 from 3.0.9 to 3.0.10
  [#895](https://github.com/strukturag/nextcloud-spreed-signaling/pull/895)
- Bump google.golang.org/protobuf from 1.36.1 to 1.36.3
  [#896](https://github.com/strukturag/nextcloud-spreed-signaling/pull/896)
- Bump golangci/golangci-lint-action from 6.1.1 to 6.2.0
  [#900](https://github.com/strukturag/nextcloud-spreed-signaling/pull/900)
- Bump golang.org/x/net from 0.30.0 to 0.33.0
  [#901](https://github.com/strukturag/nextcloud-spreed-signaling/pull/901)


## 2.0.1 - 2024-10-28

### Added
- docker: Support adding CA certificates to system trust store.
  [#825](https://github.com/strukturag/nextcloud-spreed-signaling/pull/825)
- proxy: Add timeouts to requests to Janus and cancel if session is closed.
  [#847](https://github.com/strukturag/nextcloud-spreed-signaling/pull/847)

### Changed
- make: Rename "distclean" target to "clean-generated".
  [#814](https://github.com/strukturag/nextcloud-spreed-signaling/pull/814)
- Don't update capabilities concurrently from same host.
  [#833](https://github.com/strukturag/nextcloud-spreed-signaling/pull/833)
- make: Improve dependency tracking.
  [#848](https://github.com/strukturag/nextcloud-spreed-signaling/pull/848)
- Encode session ids using protobufs.
  [#850](https://github.com/strukturag/nextcloud-spreed-signaling/pull/850)

### Fixed
- Fetch country information for continentmap from correct location.
  [#849](https://github.com/strukturag/nextcloud-spreed-signaling/pull/849)

### Dependencies
- Bump github.com/prometheus/client_golang from 1.20.2 to 1.20.3
  [#815](https://github.com/strukturag/nextcloud-spreed-signaling/pull/815)
- Bump github.com/prometheus/client_golang from 1.20.3 to 1.20.4
  [#823](https://github.com/strukturag/nextcloud-spreed-signaling/pull/823)
- Bump google.golang.org/grpc from 1.66.0 to 1.66.2
  [#818](https://github.com/strukturag/nextcloud-spreed-signaling/pull/818)
- Bump google.golang.org/grpc from 1.66.2 to 1.67.1
  [#827](https://github.com/strukturag/nextcloud-spreed-signaling/pull/827)
- Bump golangci/golangci-lint-action from 6.1.0 to 6.1.1
  [#829](https://github.com/strukturag/nextcloud-spreed-signaling/pull/829)
- Bump sphinx-rtd-theme from 2.0.0 to 3.0.0 in /docs
  [#830](https://github.com/strukturag/nextcloud-spreed-signaling/pull/830)
- Bump sphinx from 7.4.7 to 8.0.2 in /docs
  [#789](https://github.com/strukturag/nextcloud-spreed-signaling/pull/789)
- Bump github.com/nats-io/nats-server/v2 from 2.10.20 to 2.10.21
  [#826](https://github.com/strukturag/nextcloud-spreed-signaling/pull/826)
- Bump google.golang.org/protobuf from 1.34.2 to 1.35.1
  [#831](https://github.com/strukturag/nextcloud-spreed-signaling/pull/831)
- Bump jandelgado/gcov2lcov-action from 1.0.9 to 1.1.1
  [#840](https://github.com/strukturag/nextcloud-spreed-signaling/pull/840)
- Bump sphinx-rtd-theme from 3.0.0 to 3.0.1 in /docs
  [#834](https://github.com/strukturag/nextcloud-spreed-signaling/pull/834)
- Bump github.com/prometheus/client_golang from 1.20.4 to 1.20.5
  [#839](https://github.com/strukturag/nextcloud-spreed-signaling/pull/839)
- Bump github.com/nats-io/nats-server/v2 from 2.10.21 to 2.10.22
  [#843](https://github.com/strukturag/nextcloud-spreed-signaling/pull/843)
- Bump sphinx from 8.0.2 to 8.1.3 in /docs
  [#838](https://github.com/strukturag/nextcloud-spreed-signaling/pull/838)
- Bump coverallsapp/github-action from 2.3.0 to 2.3.4
  [#846](https://github.com/strukturag/nextcloud-spreed-signaling/pull/846)


## 2.0.0 - 2024-09-03

### Added
- Federation support
  [#776](https://github.com/strukturag/nextcloud-spreed-signaling/pull/776)
- CI: Add job to update generated files.
  [#790](https://github.com/strukturag/nextcloud-spreed-signaling/pull/790)
- Expose backend session limits through prometheus stats.
  [#792](https://github.com/strukturag/nextcloud-spreed-signaling/pull/792)
- CI: Test with Golang 1.23
  [#805](https://github.com/strukturag/nextcloud-spreed-signaling/pull/805)

### Changed
- Keep generated files in the repository.
  [#781](https://github.com/strukturag/nextcloud-spreed-signaling/pull/781)
- Improve caching when fetching capabilities.
  [#780](https://github.com/strukturag/nextcloud-spreed-signaling/pull/780)
- Enforce a minimum duration to cache capabilities.
  [#783](https://github.com/strukturag/nextcloud-spreed-signaling/pull/783)
- docs: Use the latest LTS of Ubuntu and Python 3.12.
  [#791](https://github.com/strukturag/nextcloud-spreed-signaling/pull/791)
- CI: Push generated code from service account.
  [#793](https://github.com/strukturag/nextcloud-spreed-signaling/pull/793)
- CI: Only build code if token exists (i.e. with Dependabot).
  [#794](https://github.com/strukturag/nextcloud-spreed-signaling/pull/794)
- CI: Always do a full build of generated files.
  [#795](https://github.com/strukturag/nextcloud-spreed-signaling/pull/795)
- Remove compatibility code for Go < 1.21.
  [#806](https://github.com/strukturag/nextcloud-spreed-signaling/pull/806)
- Send ping requests to local instance for federated sessions.
  [#808](https://github.com/strukturag/nextcloud-spreed-signaling/pull/808)

### Dependencies
- Bump sphinx from 7.3.7 to 7.4.4 in /docs
  [#773](https://github.com/strukturag/nextcloud-spreed-signaling/pull/773)
- Bump google.golang.org/grpc from 1.64.0 to 1.65.0
  [#769](https://github.com/strukturag/nextcloud-spreed-signaling/pull/769)
- Bump sphinx from 7.4.4 to 7.4.5 in /docs
  [#774](https://github.com/strukturag/nextcloud-spreed-signaling/pull/774)
- Bump github.com/nats-io/nats-server/v2 from 2.10.17 to 2.10.18
  [#775](https://github.com/strukturag/nextcloud-spreed-signaling/pull/775)
- Bump sphinx from 7.4.5 to 7.4.6 in /docs
  [#777](https://github.com/strukturag/nextcloud-spreed-signaling/pull/777)
- Bump sphinx from 7.4.6 to 7.4.7 in /docs
  [#779](https://github.com/strukturag/nextcloud-spreed-signaling/pull/779)
- Bump the etcd group with 4 updates
  [#778](https://github.com/strukturag/nextcloud-spreed-signaling/pull/778)
- Bump golangci/golangci-lint-action from 6.0.1 to 6.1.0
  [#788](https://github.com/strukturag/nextcloud-spreed-signaling/pull/788)
- Bump google.golang.org/grpc/cmd/protoc-gen-go-grpc from 1.4.0 to 1.5.1
  [#784](https://github.com/strukturag/nextcloud-spreed-signaling/pull/784)
- Bump markdown from 3.6 to 3.7 in /docs
  [#801](https://github.com/strukturag/nextcloud-spreed-signaling/pull/801)
- Bump github.com/prometheus/client_golang from 1.19.1 to 1.20.2
  [#803](https://github.com/strukturag/nextcloud-spreed-signaling/pull/803)
- Bump golang from 1.22-alpine to 1.23-alpine in /docker/server
  [#798](https://github.com/strukturag/nextcloud-spreed-signaling/pull/798)
- Bump golang from 1.22-alpine to 1.23-alpine in /docker/proxy
  [#799](https://github.com/strukturag/nextcloud-spreed-signaling/pull/799)
- Bump google.golang.org/grpc from 1.65.0 to 1.66.0
  [#810](https://github.com/strukturag/nextcloud-spreed-signaling/pull/810)
- Bump github.com/nats-io/nats-server/v2 from 2.10.18 to 2.10.19
  [#809](https://github.com/strukturag/nextcloud-spreed-signaling/pull/809)
- Bump github.com/nats-io/nats.go from 1.36.0 to 1.37.0
  [#797](https://github.com/strukturag/nextcloud-spreed-signaling/pull/797)
- Bump mkdocs from 1.6.0 to 1.6.1 in /docs
  [#812](https://github.com/strukturag/nextcloud-spreed-signaling/pull/812)
- Bump github.com/nats-io/nats-server/v2 from 2.10.19 to 2.10.20
  [#813](https://github.com/strukturag/nextcloud-spreed-signaling/pull/813)


## 1.3.2 - 2024-07-02

### Added
- Throttle /64 subnets for IPv6.
  [#750](https://github.com/strukturag/nextcloud-spreed-signaling/pull/750)
- grpc: Replace environment variables in listening address.
  [#751](https://github.com/strukturag/nextcloud-spreed-signaling/pull/751)
- Include list of supported features in websocket response.
  [#755](https://github.com/strukturag/nextcloud-spreed-signaling/pull/755)

### Changed
- Support reloading more settings
  [#752](https://github.com/strukturag/nextcloud-spreed-signaling/pull/752)
- make: Don't update CLI tools before installing.
  [#754](https://github.com/strukturag/nextcloud-spreed-signaling/pull/754)
- Don't throttle valid but expired resume requests.
  [#765](https://github.com/strukturag/nextcloud-spreed-signaling/pull/765)
- Update badge for build status to new URL.
  [#766](https://github.com/strukturag/nextcloud-spreed-signaling/pull/766)

### Fixed
- Prevent overflows when calculating throttle delay.
  [#764](https://github.com/strukturag/nextcloud-spreed-signaling/pull/764)

### Dependencies
- Bump the etcd group with 4 updates
  [#753](https://github.com/strukturag/nextcloud-spreed-signaling/pull/753)
- Bump github.com/nats-io/nats.go from 1.35.0 to 1.36.0
  [#761](https://github.com/strukturag/nextcloud-spreed-signaling/pull/761)
- Bump google.golang.org/grpc/cmd/protoc-gen-go-grpc from 1.3.0 to 1.4.0
  [#756](https://github.com/strukturag/nextcloud-spreed-signaling/pull/756)
- Bump github.com/gorilla/websocket from 1.5.1 to 1.5.3
  [#760](https://github.com/strukturag/nextcloud-spreed-signaling/pull/760)
- Bump google.golang.org/protobuf from 1.34.1 to 1.34.2
  [#759](https://github.com/strukturag/nextcloud-spreed-signaling/pull/759)
- Bump github.com/oschwald/maxminddb-golang from 1.12.0 to 1.13.0
  [#757](https://github.com/strukturag/nextcloud-spreed-signaling/pull/757)
- Bump docker/build-push-action from 5 to 6
  [#762](https://github.com/strukturag/nextcloud-spreed-signaling/pull/762)
- Bump github.com/oschwald/maxminddb-golang from 1.13.0 to 1.13.1
  [#767](https://github.com/strukturag/nextcloud-spreed-signaling/pull/767)
- Bump github.com/nats-io/nats-server/v2 from 2.10.16 to 2.10.17
  [#768](https://github.com/strukturag/nextcloud-spreed-signaling/pull/768)


## 1.3.1 - 2024-05-23

### Changed
- Bump alpine from 3.19 to 3.20 in /docker/janus
  [#746](https://github.com/strukturag/nextcloud-spreed-signaling/pull/746)
- CI: Remove deprecated options from lint workflow.
  [#748](https://github.com/strukturag/nextcloud-spreed-signaling/pull/748)
- docker: Update Janus in example image to 1.2.2
  [#749](https://github.com/strukturag/nextcloud-spreed-signaling/pull/749)
- Improve detection of actual client IP.
  [#747](https://github.com/strukturag/nextcloud-spreed-signaling/pull/747)

### Fixed
- docker: Fix proxy entrypoint.
  [#745](https://github.com/strukturag/nextcloud-spreed-signaling/pull/745)


## 1.3.0 - 2024-05-22

### Added
- Support resuming remote sessions
  [#715](https://github.com/strukturag/nextcloud-spreed-signaling/pull/715)
- Gracefully shut down signaling server on SIGUSR1.
  [#706](https://github.com/strukturag/nextcloud-spreed-signaling/pull/706)
- docker: Add helper scripts to gracefully stop / wait for server.
  [#722](https://github.com/strukturag/nextcloud-spreed-signaling/pull/722)
- Support environment variables in some configuration.
  [#721](https://github.com/strukturag/nextcloud-spreed-signaling/pull/721)
- Add Context to clients / sessions.
  [#732](https://github.com/strukturag/nextcloud-spreed-signaling/pull/732)
- Drop support for Golang 1.20
  [#737](https://github.com/strukturag/nextcloud-spreed-signaling/pull/737)
- CI: Run "govulncheck".
  [#694](https://github.com/strukturag/nextcloud-spreed-signaling/pull/694)
- Make trusted proxies configurable and default to loopback / private IPs.
  [#738](https://github.com/strukturag/nextcloud-spreed-signaling/pull/738)
- Add support for remote streams (preview)
  [#708](https://github.com/strukturag/nextcloud-spreed-signaling/pull/708)
- Add throttler for backend requests
  [#744](https://github.com/strukturag/nextcloud-spreed-signaling/pull/744)

### Changed
- build(deps): Bump github.com/nats-io/nats.go from 1.34.0 to 1.34.1
  [#697](https://github.com/strukturag/nextcloud-spreed-signaling/pull/697)
- build(deps): Bump google.golang.org/grpc from 1.62.1 to 1.63.0
  [#699](https://github.com/strukturag/nextcloud-spreed-signaling/pull/699)
- build(deps): Bump google.golang.org/grpc from 1.63.0 to 1.63.2
  [#700](https://github.com/strukturag/nextcloud-spreed-signaling/pull/700)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.10.12 to 2.10.14
  [#702](https://github.com/strukturag/nextcloud-spreed-signaling/pull/702)
- Include previous value with etcd watch events.
  [#704](https://github.com/strukturag/nextcloud-spreed-signaling/pull/704)
- build(deps): Bump go.uber.org/zap from 1.17.0 to 1.27.0
  [#705](https://github.com/strukturag/nextcloud-spreed-signaling/pull/705)
- Improve support for Janus 1.x
  [#669](https://github.com/strukturag/nextcloud-spreed-signaling/pull/669)
- build(deps): Bump sphinx from 7.2.6 to 7.3.5 in /docs
  [#709](https://github.com/strukturag/nextcloud-spreed-signaling/pull/709)
- build(deps): Bump sphinx from 7.3.5 to 7.3.7 in /docs
  [#712](https://github.com/strukturag/nextcloud-spreed-signaling/pull/712)
- build(deps): Bump golang.org/x/net from 0.21.0 to 0.23.0
  [#711](https://github.com/strukturag/nextcloud-spreed-signaling/pull/711)
- Don't keep expiration timestamp in each session.
  [#713](https://github.com/strukturag/nextcloud-spreed-signaling/pull/713)
- build(deps): Bump mkdocs from 1.5.3 to 1.6.0 in /docs
  [#714](https://github.com/strukturag/nextcloud-spreed-signaling/pull/714)
- Speedup tests by running in parallel
  [#718](https://github.com/strukturag/nextcloud-spreed-signaling/pull/718)
- build(deps): Bump golangci/golangci-lint-action from 4.0.0 to 5.0.0
  [#719](https://github.com/strukturag/nextcloud-spreed-signaling/pull/719)
- build(deps): Bump golangci/golangci-lint-action from 5.0.0 to 5.1.0
  [#720](https://github.com/strukturag/nextcloud-spreed-signaling/pull/720)
- build(deps): Bump coverallsapp/github-action from 2.2.3 to 2.3.0
  [#728](https://github.com/strukturag/nextcloud-spreed-signaling/pull/728)
- build(deps): Bump jinja2 from 3.1.3 to 3.1.4 in /docs
  [#726](https://github.com/strukturag/nextcloud-spreed-signaling/pull/726)
- build(deps): Bump google.golang.org/protobuf from 1.33.0 to 1.34.1
  [#725](https://github.com/strukturag/nextcloud-spreed-signaling/pull/725)
- build(deps): Bump github.com/prometheus/client_golang from 1.19.0 to 1.19.1
  [#730](https://github.com/strukturag/nextcloud-spreed-signaling/pull/730)
- build(deps): Bump golangci/golangci-lint-action from 5.1.0 to 6.0.1
  [#729](https://github.com/strukturag/nextcloud-spreed-signaling/pull/729)
- build(deps): Bump google.golang.org/grpc from 1.63.2 to 1.64.0
  [#734](https://github.com/strukturag/nextcloud-spreed-signaling/pull/734)
- Validate received SDP earlier.
  [#707](https://github.com/strukturag/nextcloud-spreed-signaling/pull/707)
- Log something if mcu publisher / subscriber was closed.
  [#736](https://github.com/strukturag/nextcloud-spreed-signaling/pull/736)
- build(deps): Bump the etcd group with 4 updates
  [#693](https://github.com/strukturag/nextcloud-spreed-signaling/pull/693)
- build(deps): Bump github.com/nats-io/nats.go from 1.34.1 to 1.35.0
  [#740](https://github.com/strukturag/nextcloud-spreed-signaling/pull/740)
- Don't use unnecessary pointer to "json.RawMessage".
  [#739](https://github.com/strukturag/nextcloud-spreed-signaling/pull/739)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.10.14 to 2.10.15
  [#741](https://github.com/strukturag/nextcloud-spreed-signaling/pull/741)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.10.15 to 2.10.16
  [#743](https://github.com/strukturag/nextcloud-spreed-signaling/pull/743)

### Fixed
- Improve detecting renames in file watcher.
  [#698](https://github.com/strukturag/nextcloud-spreed-signaling/pull/698)
- Update etcd watch handling.
  [#701](https://github.com/strukturag/nextcloud-spreed-signaling/pull/701)
- Prevent goroutine leaks in GRPC tests.
  [#716](https://github.com/strukturag/nextcloud-spreed-signaling/pull/716)
- Fix potential race in capabilities test.
  [#731](https://github.com/strukturag/nextcloud-spreed-signaling/pull/731)
- Don't log read error after we closed the connection.
  [#735](https://github.com/strukturag/nextcloud-spreed-signaling/pull/735)
- Fix lock order inversion when leaving room / publishing room sessions.
  [#742](https://github.com/strukturag/nextcloud-spreed-signaling/pull/742)
- Relax "MessageClientMessageData" validation.
  [#733](https://github.com/strukturag/nextcloud-spreed-signaling/pull/733)


## 1.2.4 - 2024-04-03

### Added
- Add metrics for current number of HTTP client connections.
  [#668](https://github.com/strukturag/nextcloud-spreed-signaling/pull/668)
- Support getting GeoIP DB from db-ip.com for tests.
  [#689](https://github.com/strukturag/nextcloud-spreed-signaling/pull/689)
- Use fsnotify to detect file changes
  [#680](https://github.com/strukturag/nextcloud-spreed-signaling/pull/680)
- CI: Check dependencies for minimum supported version.
  [#692](https://github.com/strukturag/nextcloud-spreed-signaling/pull/692)

### Changed
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.10.9 to 2.10.10
  [#650](https://github.com/strukturag/nextcloud-spreed-signaling/pull/650)
- CI: Also test with Golang 1.22
  [#651](https://github.com/strukturag/nextcloud-spreed-signaling/pull/651)
- build(deps): Bump the etcd group with 4 updates
  [#649](https://github.com/strukturag/nextcloud-spreed-signaling/pull/649)
- Improve Makefile
  [#653](https://github.com/strukturag/nextcloud-spreed-signaling/pull/653)
- build(deps): Bump google.golang.org/grpc from 1.61.0 to 1.61.1
  [#659](https://github.com/strukturag/nextcloud-spreed-signaling/pull/659)
- build(deps): Bump golangci/golangci-lint-action from 3.7.0 to 4.0.0
  [#658](https://github.com/strukturag/nextcloud-spreed-signaling/pull/658)
- Minor improvements to DNS monitor
  [#663](https://github.com/strukturag/nextcloud-spreed-signaling/pull/663)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.10.10 to 2.10.11
  [#662](https://github.com/strukturag/nextcloud-spreed-signaling/pull/662)
- build(deps): Bump google.golang.org/grpc from 1.61.1 to 1.62.0
  [#664](https://github.com/strukturag/nextcloud-spreed-signaling/pull/664)
- Support ports in full URLs for DNS monitor.
  [#667](https://github.com/strukturag/nextcloud-spreed-signaling/pull/667)
- Calculate proxy load based on maximum bandwidth.
  [#670](https://github.com/strukturag/nextcloud-spreed-signaling/pull/670)
- build(deps): Bump github.com/nats-io/nats.go from 1.32.0 to 1.33.1
  [#661](https://github.com/strukturag/nextcloud-spreed-signaling/pull/661)
- build(deps): Bump golang from 1.21-alpine to 1.22-alpine in /docker/server
  [#655](https://github.com/strukturag/nextcloud-spreed-signaling/pull/655)
- build(deps): Bump golang from 1.21-alpine to 1.22-alpine in /docker/proxy
  [#656](https://github.com/strukturag/nextcloud-spreed-signaling/pull/656)
- docker: Update Janus from 0.11.8 to 0.14.1.
  [#672](https://github.com/strukturag/nextcloud-spreed-signaling/pull/672)
- build(deps): Bump alpine from 3.18 to 3.19 in /docker/janus
  [#613](https://github.com/strukturag/nextcloud-spreed-signaling/pull/613)
- Reuse backoff waiting code where possible
  [#673](https://github.com/strukturag/nextcloud-spreed-signaling/pull/673)
- build(deps): Bump github.com/prometheus/client_golang from 1.18.0 to 1.19.0
  [#674](https://github.com/strukturag/nextcloud-spreed-signaling/pull/674)
- Docker improvements
  [#675](https://github.com/strukturag/nextcloud-spreed-signaling/pull/675)
- make: Don't update dependencies but use pinned versions.
  [#679](https://github.com/strukturag/nextcloud-spreed-signaling/pull/679)
- build(deps): Bump github.com/pion/sdp/v3 from 3.0.6 to 3.0.7
  [#678](https://github.com/strukturag/nextcloud-spreed-signaling/pull/678)
- build(deps): Bump google.golang.org/grpc from 1.62.0 to 1.62.1
  [#677](https://github.com/strukturag/nextcloud-spreed-signaling/pull/677)
- build(deps): Bump google.golang.org/protobuf from 1.32.0 to 1.33.0
  [#676](https://github.com/strukturag/nextcloud-spreed-signaling/pull/676)
- build(deps): Bump github.com/pion/sdp/v3 from 3.0.7 to 3.0.8
  [#681](https://github.com/strukturag/nextcloud-spreed-signaling/pull/681)
- Update source of continentmap to original CSV file.
  [#682](https://github.com/strukturag/nextcloud-spreed-signaling/pull/682)
- build(deps): Bump markdown from 3.5.2 to 3.6 in /docs
  [#684](https://github.com/strukturag/nextcloud-spreed-signaling/pull/684)
- build(deps): Bump github.com/nats-io/nats-server/v2 from 2.10.11 to 2.10.12
  [#683](https://github.com/strukturag/nextcloud-spreed-signaling/pull/683)
- build(deps): Bump github.com/pion/sdp/v3 from 3.0.8 to 3.0.9
  [#687](https://github.com/strukturag/nextcloud-spreed-signaling/pull/687)
- build(deps): Bump the etcd group with 4 updates
  [#686](https://github.com/strukturag/nextcloud-spreed-signaling/pull/686)
- build(deps): Bump github.com/nats-io/nats.go from 1.33.1 to 1.34.0
  [#685](https://github.com/strukturag/nextcloud-spreed-signaling/pull/685)
- Revert "build(deps): Bump the etcd group with 4 updates"
  [#691](https://github.com/strukturag/nextcloud-spreed-signaling/pull/691)
- CI: Limit when to run Docker build jobs.
  [#695](https://github.com/strukturag/nextcloud-spreed-signaling/pull/695)
- Remove deprecated section on multiple signaling servers from README.
  [#696](https://github.com/strukturag/nextcloud-spreed-signaling/pull/696)

### Fixed
- Fix race condition when accessing "expected" in proxy_config tests.
  [#652](https://github.com/strukturag/nextcloud-spreed-signaling/pull/652)
- Fix deadlock when entry is removed while receiver holds lock in lookup.
  [#654](https://github.com/strukturag/nextcloud-spreed-signaling/pull/654)
- Fix flaky "TestProxyConfigStaticDNS".
  [#671](https://github.com/strukturag/nextcloud-spreed-signaling/pull/671)
- Fix flaky DnsMonitor test.
  [#690](https://github.com/strukturag/nextcloud-spreed-signaling/pull/690)


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
