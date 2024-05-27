#!/usr/bin/env bash

set -e

git reset --hard origin/master

go get go.opentelemetry.io/otel
go get gopkg.in/DataDog/dd-trace-go.v1
go get github.com/davecgh/go-spew
go get github.com/pmezard/go-difflib

find . -name "*.go" | grep -E '(cmd/ghalistener|cmd/githubrunnerscalesetlistener|controllers/actions.github.com)' | xargs -I{} go-instrument -app arc -w -filename {}

git add -u
git commit -m 'chore: goinstrument'

git cherry-pick a6d6f3e
