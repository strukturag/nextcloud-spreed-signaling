FROM --platform=${BUILDPLATFORM} golang:1.24-alpine AS builder
ARG TARGETARCH
ARG TARGETOS

WORKDIR /workdir

COPY . .
RUN touch /.dockerenv && \
    apk add --no-cache bash git make && \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} make server

FROM alpine:3

ENV CONFIG=/config/server.conf
RUN adduser -D spreedbackend && \
    apk add --no-cache bash tzdata ca-certificates su-exec

COPY --from=builder /workdir/bin/signaling /usr/bin/nextcloud-spreed-signaling
COPY ./server.conf.in /config/server.conf.in
COPY ./docker/server/entrypoint.sh /
COPY ./docker/server/stop.sh /
COPY ./docker/server/wait.sh /
RUN /usr/bin/nextcloud-spreed-signaling -version

STOPSIGNAL SIGUSR1
ENTRYPOINT [ "/entrypoint.sh" ]
