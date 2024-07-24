FROM --platform=${BUILDPLATFORM} golang:1.22-alpine AS builder
ARG TARGETARCH
ARG TARGETOS

WORKDIR /workdir

COPY . .
RUN touch /.dockerenv && \
    apk add --no-cache bash git make && \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} make proxy

FROM alpine:3

ENV CONFIG=/config/proxy.conf
RUN adduser -D spreedbackend && \
    apk add --no-cache bash tzdata ca-certificates

COPY --from=builder /workdir/bin/proxy /usr/bin/nextcloud-spreed-signaling-proxy
COPY ./proxy.conf.in /config/proxy.conf.in
COPY ./docker/proxy/entrypoint.sh /
COPY ./docker/proxy/stop.sh /
COPY ./docker/proxy/wait.sh /
RUN chown spreedbackend /config
RUN /usr/bin/nextcloud-spreed-signaling-proxy -version

USER spreedbackend

STOPSIGNAL SIGUSR1
ENTRYPOINT [ "/entrypoint.sh" ]
