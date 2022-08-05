FROM golang:1.19 AS builder

WORKDIR /workdir

COPY . .
RUN apt-get -y update && \
    apt-get -y install protobuf-compiler && \
    if [ -d "vendor" ]; then GOPROXY=off make server -j$(nproc); else \
    make server -j$(nproc); fi

FROM alpine:3

ENV CONFIG=/config/server.conf
RUN adduser -D spreedbackend && \
    apk add --no-cache bash ca-certificates libc6-compat libstdc++

COPY --from=builder /workdir/bin/signaling /usr/bin/nextcloud-spreed-signaling
COPY ./server.conf.in /config/server.conf.in
COPY ./docker/server/entrypoint.sh /
RUN chown spreedbackend /config

USER spreedbackend

ENTRYPOINT [ "/entrypoint.sh" ]
CMD ["/bin/sh", "-c", "/usr/bin/nextcloud-spreed-signaling -config $CONFIG"]
