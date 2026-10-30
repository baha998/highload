package grpc_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	pb "example.com/productcatalog/gen/product/v1"
	"example.com/productcatalog/internal/repository"
	"example.com/productcatalog/internal/service"
	transport "example.com/productcatalog/internal/transport/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestGRPCIntegration(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := grpc.NewServer(grpc.ChainUnaryInterceptor(transport.Timeout(logger), transport.Idempotency(time.Minute, 100)))
	pb.RegisterProductServiceServer(server, transport.NewHandler(service.New(repository.NewMemory())))
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-serveDone; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	client := pb.NewProductServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	keyCtx := metadata.AppendToOutgoingContext(ctx, "idempotency-key", "grpc-key")
	req := &pb.CreateProductRequest{Sku: "grpc-sku", Title: "gRPC product", CategoryId: "cat-01", BrandId: "brand-01", Price: 1}
	const count = 16
	ids, failures := make(chan string, count), make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := client.CreateProduct(keyCtx, req)
			if err != nil {
				failures <- err
				return
			}
			ids <- p.Id
		}()
	}
	wg.Wait()
	close(ids)
	close(failures)
	for err := range failures {
		t.Fatalf("create: %v", err)
	}
	id := ""
	for value := range ids {
		if id == "" {
			id = value
		}
		if id != value {
			t.Fatal("different UUID on replay")
		}
	}
	if _, err := client.CreateProduct(ctx, req); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing key: %v", err)
	}
	other := &pb.CreateProductRequest{Sku: "different"}
	if _, err := client.CreateProduct(keyCtx, other); status.Code(err) != codes.Aborted {
		t.Fatalf("key conflict: %v", err)
	}
	price := 0.0
	patch := &pb.UpdateProductRequest{Id: id, Version: 1, Changes: &pb.ProductChanges{Price: &price}}
	p, err := client.UpdateProduct(ctx, patch)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != 2 || p.Price != 0 {
		t.Fatalf("update: %+v", p)
	}
	if _, err := client.UpdateProduct(ctx, patch); status.Code(err) != codes.Aborted {
		t.Fatalf("stale version: %v", err)
	}
	page, err := client.ListCategoryProducts(ctx, &pb.ListCategoryProductsRequest{CategoryId: "cat-01", Limit: 3})
	if err != nil || len(page.GetProducts()) != 3 || page.GetNextCursor() == "" {
		t.Fatalf("list=%v err=%v", page, err)
	}
	if _, err := client.GetProduct(ctx, &pb.GetProductRequest{Id: "missing"}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing: %v", err)
	}
	expired, expiredCancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer expiredCancel()
	if _, err := client.GetProduct(expired, &pb.GetProductRequest{Id: id}); status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("deadline: %v", err)
	}
}

func TestServerDeadline(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	started := time.Now()
	_, err := transport.Timeout(logger)(context.Background(), nil,
		&grpc.UnaryServerInfo{FullMethod: pb.ProductService_GetProduct_FullMethodName},
		func(ctx context.Context, _ any) (any, error) {
			deadline, ok := ctx.Deadline()
			if !ok || deadline.Sub(started) > 100*time.Millisecond {
				t.Error("server did not install the 80ms deadline")
			}
			<-ctx.Done()
			return nil, ctx.Err()
		})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
}
