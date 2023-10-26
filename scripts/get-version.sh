#!/usr/bin/env bash
set -e
ROOT="$(cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd)"

VERSION=
if [ -s "$ROOT/../version.txt" ]; then
    VERSION=$(cat "$ROOT/../version.txt" | tr -d '[:space:]')
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
fi

if [ -z "$VERSION" ]; then
    VERSION=unknown
fi

echo $VERSION
