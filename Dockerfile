# SyncFS container fleet image (dev/test).
# golang:1.26-alpine + fusermount so FUSE tests can mount inside the VM.
FROM golang:1.26-alpine

RUN apk add --no-cache fuse gcc musl-dev

WORKDIR /src
