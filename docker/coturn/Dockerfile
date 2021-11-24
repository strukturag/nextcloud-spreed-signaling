FROM alpine:3.14

RUN apk add --no-cache coturn

CMD ["/bin/sh", "-c", "turnserver --prod --fingerprint --use-auth-secret --static-auth-secret=$STATIC_SECRET --realm=$REALM --no-multicast-peers --no-tls --no-dtls"]
