// Package repository implements an isolated, concurrent in-memory catalog.
package repository

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"example.com/productcatalog/internal/domain"
)

type Memory struct {
	mu         sync.RWMutex
	products   map[string]domain.Product
	categories map[string]domain.Category
	brands     map[string]domain.Brand
	skus       map[string]string
	byCategory map[string][]string
}

func NewMemory() *Memory {
	r := &Memory{
		products: make(map[string]domain.Product), categories: make(map[string]domain.Category),
		brands: make(map[string]domain.Brand), skus: make(map[string]string), byCategory: make(map[string][]string),
	}
	// The first category contains 20 products to exercise pagination and payload measurements.
	for i := 1; i <= 10; i++ {
		id := fmt.Sprintf("cat-%02d", i)
		r.categories[id] = domain.Category{ID: id, Name: fmt.Sprintf("Category %02d", i)}
	}
	r.brands["brand-01"] = domain.Brand{ID: "brand-01", Name: "SIS Demo"}
	for i := 1; i <= 20; i++ {
		p := domain.Product{
			ID: fmt.Sprintf("product-%02d", i), SKU: fmt.Sprintf("SKU-%04d", i),
			Title: fmt.Sprintf("Catalog product %02d", i), Description: "Demo product for the SIS product catalog laboratory.",
			Price: float64(i)*100 + 0.5, CategoryID: "cat-01", BrandID: "brand-01",
			Attributes: map[string]string{"color": "black", "material": "aluminum"},
			Images:     []string{fmt.Sprintf("https://cdn.example.com/products/%02d.jpg", i)},
			InStock:    int32(i * 10), Version: 1, UpdatedAt: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC),
		}
		r.products[p.ID], r.skus[p.SKU] = p, p.ID
		r.byCategory[p.CategoryID] = append(r.byCategory[p.CategoryID], p.ID)
	}
	return r
}

func (r *Memory) Get(ctx context.Context, id string) (domain.Product, error) {
	if err := ctx.Err(); err != nil {
		return domain.Product{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return domain.Product{}, err
	}
	p, ok := r.products[id]
	if !ok {
		return domain.Product{}, domain.NewError(domain.NotFound, "product not found")
	}
	return p.Clone(), nil
}

func (r *Memory) List(ctx context.Context, category, after string, limit int) (domain.Page, error) {
	if err := ctx.Err(); err != nil {
		return domain.Page{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return domain.Page{}, err
	}
	if _, ok := r.categories[category]; !ok {
		return domain.Page{}, domain.NewError(domain.NotFound, "category not found")
	}
	ids := r.byCategory[category]
	start := sort.Search(len(ids), func(i int) bool { return ids[i] > after })
	page := domain.Page{Products: make([]domain.Product, 0, limit)}
	for i := start; i < len(ids) && len(page.Products) < limit; i++ {
		if err := ctx.Err(); err != nil {
			return domain.Page{}, err
		}
		page.Products = append(page.Products, r.products[ids[i]].Clone())
	}
	if start+len(page.Products) < len(ids) {
		page.NextCursor = page.Products[len(page.Products)-1].ID
	}
	return page, nil
}

func (r *Memory) validateReferences(p domain.Product) error {
	if _, ok := r.categories[p.CategoryID]; !ok {
		return domain.NewError(domain.Invalid, "unknown category_id")
	}
	if _, ok := r.brands[p.BrandID]; !ok {
		return domain.NewError(domain.Invalid, "unknown brand_id")
	}
	if id, ok := r.skus[p.SKU]; ok && id != p.ID {
		return domain.NewError(domain.Conflict, "sku already exists")
	}
	return nil
}

func (r *Memory) insertIndex(category, id string) {
	ids := r.byCategory[category]
	i := sort.SearchStrings(ids, id)
	ids = append(ids, "")
	copy(ids[i+1:], ids[i:])
	ids[i] = id
	r.byCategory[category] = ids
}

func (r *Memory) Create(ctx context.Context, p domain.Product) (domain.Product, error) {
	if err := ctx.Err(); err != nil {
		return domain.Product{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Product{}, err
	}
	if _, ok := r.products[p.ID]; ok {
		return domain.Product{}, domain.NewError(domain.Conflict, "id already exists")
	}
	if err := r.validateReferences(p); err != nil {
		return domain.Product{}, err
	}
	r.products[p.ID], r.skus[p.SKU] = p.Clone(), p.ID
	r.insertIndex(p.CategoryID, p.ID)
	return p.Clone(), nil
}

// Update performs compare-and-swap atomically, including unique SKU and index maintenance.
func (r *Memory) Update(ctx context.Context, p domain.Product, expected int64) (domain.Product, error) {
	if err := ctx.Err(); err != nil {
		return domain.Product{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.Product{}, err
	}
	current, ok := r.products[p.ID]
	if !ok {
		return domain.Product{}, domain.NewError(domain.NotFound, "product not found")
	}
	if current.Version != expected {
		return domain.Product{}, domain.NewError(domain.Conflict, "version conflict; re-read product")
	}
	if err := r.validateReferences(p); err != nil {
		return domain.Product{}, err
	}
	if current.CategoryID != p.CategoryID {
		ids := r.byCategory[current.CategoryID]
		i := sort.SearchStrings(ids, p.ID)
		r.byCategory[current.CategoryID] = append(ids[:i], ids[i+1:]...)
		r.insertIndex(p.CategoryID, p.ID)
	}
	delete(r.skus, current.SKU)
	p.Version = current.Version + 1
	r.products[p.ID], r.skus[p.SKU] = p.Clone(), p.ID
	return p.Clone(), nil
}
