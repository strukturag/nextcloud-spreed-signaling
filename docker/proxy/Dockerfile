FROM --platform=${BUILDPLATFORM} golang:1.20-alpine AS builder
ARG TARGETARCH
ARG TARGETOS

WORKDIR /workdir

COPY . .
RUN apk add --no-cache bash build-base protobuf && \
    if [ -d "vendor" ]; then GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOPROXY=off make proxy -j$(nproc); else \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} make proxy -j$(nproc); fi

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
CMD ["/bin/sh", "-c", "/usr/bin/nextcloud-spreed-signaling-proxy -config $CONFIG"]
