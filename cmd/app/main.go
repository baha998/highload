package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	pb "example.com/productcatalog/gen/product/v1"
	"example.com/productcatalog/internal/repository"
	"example.com/productcatalog/internal/service"
	grpctransport "example.com/productcatalog/internal/transport/grpc"
	"example.com/productcatalog/internal/transport/rest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		logger.Error("application stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	catalog := service.New(repository.NewMemory())
	httpServer := &http.Server{Addr: ":8080", Handler: rest.NewServerHandler(catalog, logger),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	httpListener, err := net.Listen("tcp", httpServer.Addr)
	if err != nil {
		return fmt.Errorf("HTTP listen: %w", err)
	}
	grpcListener, err := net.Listen("tcp", ":50051")
	if err != nil {
		if closeErr := httpListener.Close(); closeErr != nil {
			logger.Error("close HTTP listener", "error", closeErr)
		}
		return fmt.Errorf("gRPC listen: %w", err)
	}
	grpcServer := grpc.NewServer(grpc.MaxRecvMsgSize(1<<20), grpc.ChainUnaryInterceptor(
		grpctransport.Timeout(logger), grpctransport.Idempotency(10*time.Minute, 10000)))
	pb.RegisterProductServiceServer(grpcServer, grpctransport.NewHandler(catalog))
	reflection.Register(grpcServer)
	failures := make(chan error, 2)
	go func() { failures <- httpServer.Serve(httpListener) }()
	go func() { failures <- grpcServer.Serve(grpcListener) }()
	logger.Info("catalog started", "http", ":8080", "grpc", ":50051", "products", 20, "categories", 10)
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-failures:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	grpcDone := make(chan struct{})
	go func() { grpcServer.GracefulStop(); close(grpcDone) }()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP shutdown", "error", err)
		if closeErr := httpServer.Close(); closeErr != nil {
			logger.Error("HTTP close", "error", closeErr)
		}
	}
	select {
	case <-grpcDone:
	case <-shutdownCtx.Done():
		grpcServer.Stop()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) && !errors.Is(serveErr, grpc.ErrServerStopped) {
		return serveErr
	}
	return nil
}
