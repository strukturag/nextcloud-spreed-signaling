FROM --platform=${BUILDPLATFORM} golang:1.20 AS builder

WORKDIR /workdir

ARG TARGETARCH
ARG TARGETOS

COPY . .
RUN apt-get -y update && \
    apt-get -y install protobuf-compiler && \
    if [ -d "vendor" ]; then GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOPROXY=off make server -j$(nproc); else \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} make server -j$(nproc); fi

FROM debian:bullseye-slim

ENV CONFIG=/config/server.conf
RUN adduser --system --no-create-home spreedbackend && \
    apt-get update -y && \
    apt-get install -y ca-certificates && \
    rm -rf /var/apt/lists/*

COPY --from=builder /workdir/bin/signaling /usr/bin/nextcloud-spreed-signaling
COPY ./server.conf.in /config/server.conf.in
COPY ./docker/server/entrypoint.sh /
RUN chown spreedbackend /config

USER spreedbackend

ENTRYPOINT [ "/entrypoint.sh" ]
CMD ["/bin/sh", "-c", "/usr/bin/nextcloud-spreed-signaling -config $CONFIG"]
