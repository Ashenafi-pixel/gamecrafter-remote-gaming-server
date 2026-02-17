#!/bin/sh
set -e
export GO111MODULE=on

# Find repo root (directory containing go.mod) - Render may run build from another cwd
while [ "$(pwd)" != "/" ]; do
  if [ -f go.mod ]; then
    break
  fi
  cd ..
done
if [ ! -f go.mod ]; then
  echo "render-build.sh: go.mod not found (run from repo root or set Root Directory empty)"
  exit 1
fi

go mod download
go build -ldflags '-s -w' -o app ./cmd/server
