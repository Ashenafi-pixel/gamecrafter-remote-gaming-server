#!/bin/sh
set -e
# Force module mode and run from directory containing this script (repo root)
export GO111MODULE=on
cd "$(dirname "$0")"
go mod download
go build -ldflags '-s -w' -o app ./cmd/server
