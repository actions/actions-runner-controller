#!/usr/bin/env bash

COMMIT=$(git rev-parse HEAD)
TAG=$(git describe --exact-match --abbrev=0 --tags "${COMMIT}" 2> /dev/null || true)
BRANCH=$(git branch | grep \* | cut -d ' ' -f2 | sed -e 's/[^a-zA-Z0-9+=._:/-]*//g' || true)
VERSION=""

if [ -z "$TAG" ]; then
  [[ -n "$BRANCH" ]] && VERSION="${BRANCH}-"
	VERSION="${VERSION}${COMMIT:0:8}"
else
	VERSION=$TAG
fi

if [ -n "$(git diff --shortstat 2> /dev/null | tail -n1)" ]; then
    VERSION="${VERSION}-dirty"
fi

export VERSION
