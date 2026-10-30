#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
ghz --insecure \
  --proto proto/product.proto \
  --call catalog.v1.ProductService.GetProduct \
  --data '{"id":"product-01"}' \
  --rps 3000 \
  --connections 1 \
  --concurrency 128 \
  --duration "${DURATION:-30s}" \
  --format json \
  "${GRPC_TARGET:-localhost:50051}"
