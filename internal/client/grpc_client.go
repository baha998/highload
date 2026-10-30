// Package client provides a gRPC connection with bounded, cancellation-aware retries.
package client

import (
	"context"
	"math/rand/v2"
	"time"

	pb "example.com/productcatalog/gen/product/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// New uses plaintext only for the local laboratory. The caller owns conn.Close().
func New(target string) (pb.ProductServiceClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDisableRetry(), grpc.WithUnaryInterceptor(RetryInterceptor()))
	if err != nil {
		return nil, nil, err
	}
	return pb.NewProductServiceClient(conn), conn, nil
}

func RetryInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		// PATCH-like updates have version checks, but replaying their success is not implemented.
		safe := method == pb.ProductService_GetProduct_FullMethodName || method == pb.ProductService_ListCategoryProducts_FullMethodName
		if method == pb.ProductService_CreateProduct_FullMethodName {
			md, _ := metadata.FromOutgoingContext(ctx)
			keys := md.Get("idempotency-key")
			safe = len(keys) == 1 && keys[0] != ""
		}
		for attempt := 0; attempt < 3; attempt++ {
			if err := ctx.Err(); err != nil {
				return status.FromContextError(err).Err()
			}
			err := invoker(ctx, method, req, reply, conn, opts...)
			if err == nil {
				return nil
			}
			if !safe || status.Code(err) != codes.Unavailable || attempt == 2 {
				return err
			}
			// Full jitter: uniform [0, base*2^attempt), 5ms then 10ms maximum.
			backoff := 5 * time.Millisecond << attempt
			timer := time.NewTimer(time.Duration(rand.Int64N(int64(backoff))))
			select {
			case <-ctx.Done():
				timer.Stop()
				return status.FromContextError(ctx.Err()).Err()
			case <-timer.C:
			}
		}
		return nil
	}
}
