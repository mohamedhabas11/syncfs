#!/bin/sh
# Bootstrap a 3-voter test fleet: generates CA + node certs/configs.
# Usage: ./scripts/bootstrap-fleet.sh <cluster-id> <out-dir> [peer addrs] [http addrs]
set -eu
CLUSTER_ID="${1:?cluster-id required}"
OUT_DIR="${2:?out dir required}"
PEERS="${3:-127.0.0.1:5001,127.0.0.1:5002,127.0.0.1:5003}"
HTTP="${4:-127.0.0.1:18081,127.0.0.1:18082,127.0.0.1:18083}"
mkdir -p "$OUT_DIR"
go run ./cmd/syncfs admin bootstrap --cluster-id "$CLUSTER_ID" --out "$OUT_DIR" --peers "$PEERS" --http-addrs "$HTTP"
echo "Fleet ready in $OUT_DIR:"
ls -l "$OUT_DIR"
