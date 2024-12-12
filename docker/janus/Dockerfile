# Modified from https://gitlab.com/powerpaul17/nc_talk_backend/-/blob/dcbb918d8716dad1eb72a889d1e6aa1e3a543641/docker/janus/Dockerfile

FROM alpine:3

RUN apk add --no-cache curl autoconf automake libtool pkgconf build-base \
  glib-dev libconfig-dev libnice-dev jansson-dev openssl-dev zlib libsrtp-dev \
  gengetopt libwebsockets-dev git curl-dev libogg-dev

# usrsctp
# 03 Nov 2024
ARG USRSCTP_VERSION=b28f0b55b00bde67f6be80d6623e2775b88026b8

RUN cd /tmp && \
    git clone https://github.com/sctplab/usrsctp && \
    cd usrsctp && \
    git checkout $USRSCTP_VERSION && \
    ./bootstrap && \
    ./configure --prefix=/usr && \
    make -j$(nproc) && make install

# libsrtp
ARG LIBSRTP_VERSION=2.6.0
RUN cd /tmp && \
    wget https://github.com/cisco/libsrtp/archive/v$LIBSRTP_VERSION.tar.gz && \
    tar xfv v$LIBSRTP_VERSION.tar.gz && \
    cd libsrtp-$LIBSRTP_VERSION && \
    ./configure --prefix=/usr --enable-openssl && \
    make shared_library -j$(nproc) && \
    make install && \
    rm -fr /libsrtp-$LIBSRTP_VERSION && \
    rm -f /v$LIBSRTP_VERSION.tar.gz

# JANUS

ARG JANUS_VERSION=1.3.0
RUN mkdir -p /usr/src/janus && \
    cd /usr/src/janus && \
    curl -L https://github.com/meetecho/janus-gateway/archive/v$JANUS_VERSION.tar.gz | tar -xz && \
    cd /usr/src/janus/janus-gateway-$JANUS_VERSION && \
    ./autogen.sh && \
    ./configure --disable-rabbitmq --disable-mqtt --disable-boringssl && \
    make -j$(nproc) && \
    make install && \
    make configs

WORKDIR /usr/src/janus/janus-gateway-$JANUS_VERSION

CMD [ "janus" ]
