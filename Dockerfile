FROM golang:1.18 AS builder

WORKDIR /workdir

COPY . .
RUN make build

FROM alpine:3.15

ENV CONFIG=/config/server.conf
RUN adduser -D spreedbackend && \
    apk add --no-cache ca-certificates libc6-compat libstdc++
USER spreedbackend
COPY --from=builder /workdir/bin/signaling /usr/local/signaling
COPY ./server.conf.in /config/server.conf

CMD ["/bin/sh", "-c", "/usr/local/signaling --config=$CONFIG"]
