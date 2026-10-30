// Command measure records payload sizes and an open-loop loopback load test.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"runtime"
	"sort"
	"sync"
	"time"

	pb "example.com/productcatalog/gen/product/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

type measurement struct {
	Protocol     string  `json:"protocol"`
	Requests     int     `json:"requests"`
	Errors       int     `json:"errors"`
	OfferedRPS   int     `json:"offered_rps"`
	CompletedRPS float64 `json:"completed_rps"`
	P50          float64 `json:"p50_ms"`
	P95          float64 `json:"p95_ms"`
	P99          float64 `json:"p99_ms"`
}

type payload struct {
	Products int `json:"products"`
	JSON     int `json:"json_bytes"`
	Protobuf int `json:"protobuf_bytes"`
}

type report struct {
	Time         time.Time     `json:"time"`
	Go           string        `json:"go"`
	OS           string        `json:"os"`
	Arch         string        `json:"arch"`
	CPUs         int           `json:"logical_cpus"`
	Duration     string        `json:"duration_per_protocol"`
	Workers      int           `json:"workers"`
	Payloads     []payload     `json:"payloads"`
	Measurements []measurement `json:"measurements"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	httpTarget := flag.String("http", "http://localhost:8080", "REST base URL")
	grpcTarget := flag.String("grpc", "localhost:50051", "gRPC target")
	rate := flag.Int("rate", 3000, "offered requests per second")
	duration := flag.Duration("duration", 10*time.Second, "duration per protocol")
	workers := flag.Int("workers", 128, "concurrent workers")
	flag.Parse()
	if *rate < 1 || *rate > 100000 || *duration < time.Second || *duration > time.Minute || *workers < 1 || *workers > 1000 {
		return fmt.Errorf("rate 1..100000, duration 1s..1m and workers 1..1000 required")
	}
	transport := &http.Transport{MaxIdleConns: *workers, MaxIdleConnsPerHost: *workers, MaxConnsPerHost: *workers}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: time.Second}
	conn, err := grpc.NewClient(*grpcTarget, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDisableRetry())
	if err != nil {
		return err
	}
	defer func() {
		if err := conn.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close gRPC:", err)
		}
	}()
	grpcClient := pb.NewProductServiceClient(conn)
	fetch := func(path string) ([]byte, error) {
		response, err := httpClient.Get(*httpTarget + path)
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("HTTP %d: %s", response.StatusCode, data)
		}
		return data, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	oneJSON, err := fetch("/v1/products/product-01")
	if err != nil {
		return err
	}
	oneProto, err := grpcClient.GetProduct(ctx, &pb.GetProductRequest{Id: "product-01"})
	if err != nil {
		return err
	}
	listJSON, err := fetch("/v1/categories/cat-01/products?limit=20")
	if err != nil {
		return err
	}
	listProto, err := grpcClient.ListCategoryProducts(ctx, &pb.ListCategoryProductsRequest{CategoryId: "cat-01", Limit: 20})
	if err != nil {
		return err
	}
	r := report{Time: time.Now().UTC(), Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH,
		CPUs: runtime.NumCPU(), Duration: duration.String(), Workers: *workers,
		Payloads: []payload{{1, len(oneJSON), proto.Size(oneProto)}, {len(listProto.Products), len(listJSON), proto.Size(listProto)}}}
	restCall := func() error { _, err := fetch("/v1/products/product-01"); return err }
	grpcCall := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := grpcClient.GetProduct(ctx, &pb.GetProductRequest{Id: "product-01"})
		return err
	}
	for _, target := range []struct {
		name string
		call func() error
	}{{"REST", restCall}, {"gRPC", grpcCall}} {
		for i := 0; i < 200; i++ {
			if err := target.call(); err != nil {
				return fmt.Errorf("warmup %s: %w", target.name, err)
			}
		}
		r.Measurements = append(r.Measurements, measure(target.name, *rate, *duration, *workers, target.call))
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(r)
}

func measure(name string, rate int, duration time.Duration, workers int, call func() error) measurement {
	count := int(float64(rate) * duration.Seconds())
	type sample struct {
		latency time.Duration
		err     error
	}
	jobs := make(chan time.Time, count)
	samples := make(chan sample, count)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for scheduled := range jobs {
				err := call()
				// Include scheduling and queue delay to avoid coordinated omission.
				samples <- sample{time.Since(scheduled), err}
			}
		}()
	}
	start := time.Now()
	for i := 0; i < count; i++ {
		scheduled := start.Add(time.Duration(int64(i) * int64(time.Second) / int64(rate)))
		if delay := time.Until(scheduled); delay > 0 {
			time.Sleep(delay)
		}
		jobs <- scheduled
	}
	close(jobs)
	wg.Wait()
	elapsed := time.Since(start)
	close(samples)
	latencies := make([]time.Duration, 0, count)
	errors := 0
	for sample := range samples {
		latencies = append(latencies, sample.latency)
		if sample.err != nil {
			errors++
		}
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	percentile := func(p float64) float64 {
		return float64(latencies[int(math.Ceil(p*float64(len(latencies))))-1]) / float64(time.Millisecond)
	}
	return measurement{Protocol: name, Requests: count, Errors: errors, OfferedRPS: rate,
		CompletedRPS: float64(count) / elapsed.Seconds(), P50: percentile(.50), P95: percentile(.95), P99: percentile(.99)}
}
