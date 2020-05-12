#!/bin/bash
set -e
ROOT="$(cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd)"

VERSION=
if [ -s "$ROOT/../version.txt" ]; then
    VERSION=$(cat "$ROOT/../version.txt" | tr -d '[:space:]')
fi
if [ -z $VERSION ] && [ -d "$ROOT/../.git" ]; then
    if [ "$1" == "--tar" ]; then
        VERSION=$(git describe --dirty --tags --always | sed 's/debian\///g')
    else
        VERSION=$(git log -1 --pretty=%H)
    fi
fi

if [ -z $VERSION ]; then
    VERSION=unknown
fi

echo $VERSION
