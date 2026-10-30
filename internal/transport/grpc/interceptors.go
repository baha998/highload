package grpc

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"strings"
	"time"

	pb "example.com/productcatalog/gen/product/v1"
	"example.com/productcatalog/internal/domain"
	"example.com/productcatalog/internal/idempotency"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

// Timeout is the outer interceptor and maps all inner errors to public gRPC statuses.
func Timeout(logger *slog.Logger) grpcgo.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpcgo.UnaryServerInfo, handler grpcgo.UnaryHandler) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
		defer cancel()
		response, err := handler(ctx, req)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, toStatus(ctxErr, logger)
		}
		if err != nil {
			return nil, toStatus(err, logger)
		}
		return response, nil
	}
}

func Idempotency(ttl time.Duration, capacity int) grpcgo.UnaryServerInterceptor {
	cache := idempotency.New[proto.Message](ttl, capacity)
	return func(ctx context.Context, req any, info *grpcgo.UnaryServerInfo, handler grpcgo.UnaryHandler) (any, error) {
		if info.FullMethod != pb.ProductService_CreateProduct_FullMethodName {
			return handler(ctx, req)
		}
		md, _ := metadata.FromIncomingContext(ctx)
		keys := md.Get("idempotency-key")
		if len(keys) != 1 || strings.TrimSpace(keys[0]) == "" || len(keys[0]) > 128 {
			return nil, domain.NewError(domain.Invalid, "one idempotency-key of 1..128 bytes is required")
		}
		message, ok := req.(proto.Message)
		if !ok {
			return nil, domain.NewError(domain.Invalid, "expected protobuf request")
		}
		data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
		if err != nil {
			return nil, err
		}
		result, err := cache.Do(ctx, keys[0], sha256.Sum256(data), func() (proto.Message, error) {
			response, err := handler(ctx, req)
			if err != nil {
				return nil, err
			}
			message, ok := response.(proto.Message)
			if !ok {
				return nil, domain.NewError(domain.Invalid, "expected protobuf response")
			}
			return proto.Clone(message), nil
		})
		if err != nil {
			return nil, err
		}
		return proto.Clone(result), nil
	}
}
