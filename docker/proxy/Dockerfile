FROM --platform=${BUILDPLATFORM} golang:1.20 AS builder
ARG TARGETARCH
ARG TARGETOS

WORKDIR /workdir

COPY . .
RUN apt-get -y update && \
    apt-get -y install protobuf-compiler && \
    if [ -d "vendor" ]; then GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOPROXY=off make proxy -j$(nproc); else \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} make proxy -j$(nproc); fi

FROM debian:bullseye-slim

ENV CONFIG=/config/proxy.conf
RUN adduser --system --no-create-home spreedbackend && \
    apt-get update -y && \
    apt-get install -y ca-certificates && \
    rm -rf /var/apt/lists/*

COPY --from=builder /workdir/bin/proxy /usr/bin/nextcloud-spreed-signaling-proxy
COPY ./proxy.conf.in /config/proxy.conf.in
COPY ./docker/proxy/entrypoint.sh /
RUN chown spreedbackend /config

USER spreedbackend

ENTRYPOINT [ "/entrypoint.sh" ]
CMD ["/bin/sh", "-c", "/usr/bin/nextcloud-spreed-signaling-proxy -config $CONFIG"]
