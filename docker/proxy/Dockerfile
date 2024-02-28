FROM --platform=${BUILDPLATFORM} golang:1.22-alpine AS builder
ARG TARGETARCH
ARG TARGETOS

WORKDIR /workdir

COPY . .
RUN touch /.dockerenv && \
    apk add --no-cache bash git build-base protobuf && \
    if [ -d "vendor" ]; then GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOPROXY=off make proxy; else \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} make proxy; fi

FROM alpine:3

ENV CONFIG=/config/proxy.conf
RUN adduser -D spreedbackend && \
    apk add --no-cache bash tzdata ca-certificates

COPY --from=builder /workdir/bin/proxy /usr/bin/nextcloud-spreed-signaling-proxy
COPY ./proxy.conf.in /config/proxy.conf.in
COPY ./docker/proxy/entrypoint.sh /
RUN chown spreedbackend /config
RUN /usr/bin/nextcloud-spreed-signaling-proxy -version

USER spreedbackend

ENTRYPOINT [ "/entrypoint.sh" ]
