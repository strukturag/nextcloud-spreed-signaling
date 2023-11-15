#!/usr/bin/env bash
set -e
ROOT="$(cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd)"

VERSION=
if [ -s "$ROOT/../version.txt" ]; then
    VERSION=$(tr -d '[:space:]' < "$ROOT/../version.txt")
fi
if [ -z "$VERSION" ] && [ -d "$ROOT/../.git" ]; then
    TAG=$(git tag --points-at HEAD | sed 's/v//')
    if [ "$1" == "--tar" ]; then
        VERSION=$(git describe --dirty --tags --always | sed 's/debian\///g')
    elif [ -n "$TAG" ]; then
        VERSION="$TAG"
    else
        VERSION=$(git log -1 --pretty=%H)
    fi
    if [ -f "/.dockerenv" ]; then
        VERSION="$VERSION~docker"
    elif grep -sq 'docker\|lxc' /proc/1/cgroup; then
        VERSION="$VERSION~docker"
    fi
fi

if [ -z "$VERSION" ]; then
    VERSION=unknown
fi

echo $VERSION
