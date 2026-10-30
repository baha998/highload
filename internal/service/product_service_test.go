package service_test

import (
	"context"
	"sync"
	"testing"

	"example.com/productcatalog/internal/domain"
	"example.com/productcatalog/internal/repository"
	"example.com/productcatalog/internal/service"
)

func TestConcurrentVersionCheck(t *testing.T) {
	s := service.New(repository.NewMemory())
	const count = 32
	errors := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			price := 777.0
			_, err := s.Update(context.Background(), "product-01", domain.UpdateProduct{Version: 1, Changes: domain.Changes{Price: &price}})
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	successes := 0
	for err := range errors {
		if err == nil {
			successes++
			continue
		}
		if domain.KindOf(err) != domain.Conflict {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("want one winner, got %d", successes)
	}
}

func TestPaginationAndIsolation(t *testing.T) {
	s := service.New(repository.NewMemory())
	ctx := context.Background()
	seen := make(map[string]bool)
	token := ""
	for {
		page, err := s.List(ctx, "cat-01", token, 3)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range page.Products {
			if seen[p.ID] {
				t.Fatalf("duplicate: %s", p.ID)
			}
			seen[p.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		if _, err := s.List(ctx, "cat-02", page.NextCursor, 3); domain.KindOf(err) != domain.Invalid {
			t.Fatal("cross-category cursor accepted")
		}
		token = page.NextCursor
	}
	if len(seen) != 20 {
		t.Fatalf("want 20 products, got %d", len(seen))
	}
	p, err := s.Get(ctx, "product-01")
	if err != nil {
		t.Fatal(err)
	}
	p.Attributes["color"], p.Images[0] = "modified", "modified"
	p, err = s.Get(ctx, "product-01")
	if err != nil {
		t.Fatal(err)
	}
	if p.Attributes["color"] != "black" || p.Images[0] == "modified" {
		t.Fatal("repository leaked mutable state")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Get(canceled, "product-01"); err != context.Canceled {
		t.Fatalf("want context cancellation, got %v", err)
	}
}

func TestMoveCategoryMaintainsIndex(t *testing.T) {
	s := service.New(repository.NewMemory())
	category := "cat-02"
	_, err := s.Update(context.Background(), "product-01", domain.UpdateProduct{Version: 1, Changes: domain.Changes{CategoryID: &category}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.List(context.Background(), category, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Products) != 1 || page.Products[0].ID != "product-01" {
		t.Fatalf("invalid index: %+v", page)
	}
}
