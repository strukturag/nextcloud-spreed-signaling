FROM --platform=${BUILDPLATFORM} golang:1.22-alpine AS builder
ARG TARGETARCH
ARG TARGETOS

WORKDIR /workdir

COPY . .
RUN touch /.dockerenv && \
    apk add --no-cache bash git build-base protobuf && \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} make server

FROM alpine:3

ENV CONFIG=/config/server.conf
RUN adduser -D spreedbackend && \
    apk add --no-cache bash tzdata ca-certificates

COPY --from=builder /workdir/bin/signaling /usr/bin/nextcloud-spreed-signaling
COPY ./server.conf.in /config/server.conf.in
COPY ./docker/server/entrypoint.sh /
COPY ./docker/server/stop.sh /
COPY ./docker/server/wait.sh /
RUN chown spreedbackend /config
RUN /usr/bin/nextcloud-spreed-signaling -version

USER spreedbackend

STOPSIGNAL SIGUSR1
ENTRYPOINT [ "/entrypoint.sh" ]
