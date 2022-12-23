FROM --platform=${BUILDPLATFORM} golang:1.19 AS builder
ARG TARGETARCH
ARG TARGETOS

WORKDIR /workdir

COPY . .
RUN apt-get -y update && \
    apt-get -y install protobuf-compiler && \
    if [ -d "vendor" ]; then GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOPROXY=off make proxy -j$(nproc); else \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} make proxy -j$(nproc); fi

FROM alpine:3

ENV CONFIG=/config/proxy.conf
RUN adduser -D spreedbackend && \
    apk add --no-cache bash ca-certificates libc6-compat libstdc++

COPY --from=builder /workdir/bin/proxy /usr/bin/nextcloud-spreed-signaling-proxy
COPY ./proxy.conf.in /config/proxy.conf.in
COPY ./docker/proxy/entrypoint.sh /
RUN chown spreedbackend /config

USER spreedbackend

ENTRYPOINT [ "/entrypoint.sh" ]
CMD ["/bin/sh", "-c", "/usr/bin/nextcloud-spreed-signaling-proxy -config $CONFIG"]
