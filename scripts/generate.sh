#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export GOBIN="$PWD/.tools/bin"
mkdir -p "$GOBIN"
go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.34.2
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.4.0
export PATH="$GOBIN:$PATH"
protoc -I proto --go_out=. --go_opt=module=example.com/productcatalog \
  --go-grpc_out=. --go-grpc_opt=module=example.com/productcatalog proto/product.proto
