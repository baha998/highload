package client_test

import (
	"context"
	"testing"

	pb "example.com/productcatalog/gen/product/v1"
	"example.com/productcatalog/internal/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestRetryPolicy(t *testing.T) {
	tests := []struct {
		name, method string
		code         codes.Code
		key          bool
		want         int
	}{
		{"read unavailable", pb.ProductService_GetProduct_FullMethodName, codes.Unavailable, false, 3},
		{"read invalid", pb.ProductService_GetProduct_FullMethodName, codes.InvalidArgument, false, 1},
		{"create without key", pb.ProductService_CreateProduct_FullMethodName, codes.Unavailable, false, 1},
		{"create with key", pb.ProductService_CreateProduct_FullMethodName, codes.Unavailable, true, 3},
		{"update", pb.ProductService_UpdateProduct_FullMethodName, codes.Unavailable, false, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.key {
				ctx = metadata.AppendToOutgoingContext(ctx, "idempotency-key", "stable-key")
			}
			calls := 0
			err := client.RetryInterceptor()(ctx, tc.method, nil, nil, nil,
				func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
					calls++
					return status.Error(tc.code, "temporary")
				})
			if calls != tc.want || status.Code(err) != tc.code {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := client.RetryInterceptor()(ctx, pb.ProductService_GetProduct_FullMethodName, nil, nil, nil,
		func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
			t.Fatal("invoked after cancellation")
			return nil
		})
	if status.Code(err) != codes.Canceled {
		t.Fatalf("want canceled, got %v", err)
	}
}
