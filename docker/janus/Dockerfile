# Modified from https://gitlab.com/powerpaul17/nc_talk_backend/-/blob/dcbb918d8716dad1eb72a889d1e6aa1e3a543641/docker/janus/Dockerfile
FROM alpine:3.14

RUN apk add --no-cache curl autoconf automake libtool pkgconf build-base \
  glib-dev libconfig-dev libnice-dev jansson-dev openssl-dev zlib libsrtp-dev \
  gengetopt libwebsockets-dev git curl-dev libogg-dev

# usrsctp
# 08 Oct 2021
ARG USRSCTP_VERSION=7c31bd35c79ba67084ce029511193a19ceb97447

RUN cd /tmp && \
    git clone https://github.com/sctplab/usrsctp && \
    cd usrsctp && \
    git checkout $USRSCTP_VERSION && \
    ./bootstrap && \
    ./configure --prefix=/usr && \
    make && make install

# libsrtp
ARG LIBSRTP_VERSION=2.4.2
RUN cd /tmp && \
    wget https://github.com/cisco/libsrtp/archive/v$LIBSRTP_VERSION.tar.gz && \
    tar xfv v$LIBSRTP_VERSION.tar.gz && \
    cd libsrtp-$LIBSRTP_VERSION && \
    ./configure --prefix=/usr --enable-openssl && \
    make shared_library && \
    make install && \
    rm -fr /libsrtp-$LIBSRTP_VERSION && \
    rm -f /v$LIBSRTP_VERSION.tar.gz

# JANUS

ARG JANUS_VERSION=0.11.8
RUN mkdir -p /usr/src/janus && \
    cd /usr/src/janus && \
    curl -L https://github.com/meetecho/janus-gateway/archive/v$JANUS_VERSION.tar.gz | tar -xz && \
    cd /usr/src/janus/janus-gateway-$JANUS_VERSION && \
    ./autogen.sh && \
    ./configure --disable-rabbitmq --disable-mqtt --disable-boringssl && \
    make && \
    make install && \
    make configs

WORKDIR /usr/src/janus/janus-gateway-$JANUS_VERSION

CMD [ "janus" ]
