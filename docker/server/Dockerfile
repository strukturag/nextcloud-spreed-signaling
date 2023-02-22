FROM --platform=${BUILDPLATFORM} golang:1.20-alpine AS builder
ARG TARGETARCH
ARG TARGETOS

WORKDIR /workdir

COPY . .
RUN apk add --no-cache bash build-base protobuf && \
    if [ -d "vendor" ]; then GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOPROXY=off make server -j$(nproc); else \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} make server -j$(nproc); fi

FROM alpine:3

ENV CONFIG=/config/server.conf
RUN adduser -D spreedbackend && \
    apk add --no-cache bash tzdata ca-certificates

COPY --from=builder /workdir/bin/signaling /usr/bin/nextcloud-spreed-signaling
COPY ./server.conf.in /config/server.conf.in
COPY ./docker/server/entrypoint.sh /
RUN chown spreedbackend /config
RUN /usr/bin/nextcloud-spreed-signaling -version

USER spreedbackend

ENTRYPOINT [ "/entrypoint.sh" ]
CMD ["/bin/sh", "-c", "/usr/bin/nextcloud-spreed-signaling -config $CONFIG"]
