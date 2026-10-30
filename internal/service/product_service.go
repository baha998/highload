// Package service owns validation, cursor semantics and catalog use cases.
package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"example.com/productcatalog/internal/domain"
)

type Repository interface {
	Get(context.Context, string) (domain.Product, error)
	List(context.Context, string, string, int) (domain.Page, error)
	Create(context.Context, domain.Product) (domain.Product, error)
	Update(context.Context, domain.Product, int64) (domain.Product, error)
}

type ProductService struct{ repo Repository }

func New(repo Repository) *ProductService { return &ProductService{repo: repo} }

func checkContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (s *ProductService) Get(ctx context.Context, id string) (domain.Product, error) {
	if err := checkContext(ctx); err != nil {
		return domain.Product{}, err
	}
	if strings.TrimSpace(id) == "" {
		return domain.Product{}, domain.NewError(domain.Invalid, "id is required")
	}
	return s.repo.Get(ctx, id)
}

type cursor struct {
	Category string `json:"category"`
	After    string `json:"after"`
}

func (s *ProductService) List(ctx context.Context, category, token string, limit int) (domain.Page, error) {
	if err := checkContext(ctx); err != nil {
		return domain.Page{}, err
	}
	if category == "" || limit < 0 || limit > 100 {
		return domain.Page{}, domain.NewError(domain.Invalid, "category required; limit must be 0..100")
	}
	if limit == 0 {
		limit = 20
	}
	var c cursor
	if token != "" {
		if len(token) > 2048 {
			return domain.Page{}, domain.NewError(domain.Invalid, "cursor too long")
		}
		data, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			return domain.Page{}, domain.NewError(domain.Invalid, "invalid cursor encoding")
		}
		if err := json.Unmarshal(data, &c); err != nil {
			return domain.Page{}, domain.NewError(domain.Invalid, "invalid cursor")
		}
		if c.Category != category || c.After == "" {
			return domain.Page{}, domain.NewError(domain.Invalid, "cursor does not belong to category")
		}
	}
	page, err := s.repo.List(ctx, category, c.After, limit)
	if err != nil {
		return domain.Page{}, err
	}
	if page.NextCursor != "" {
		data, err := json.Marshal(cursor{Category: category, After: page.NextCursor})
		if err != nil {
			return domain.Page{}, fmt.Errorf("encode cursor: %w", err)
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return page, nil
}

func validate(p domain.Product) error {
	if strings.TrimSpace(p.SKU) == "" || strings.TrimSpace(p.Title) == "" || p.CategoryID == "" || p.BrandID == "" {
		return domain.NewError(domain.Invalid, "sku, title, category_id and brand_id are required")
	}
	if len(p.SKU) > 128 || len(p.Title) > 512 || len(p.Description) > 16384 || len(p.CategoryID) > 128 || len(p.BrandID) > 128 {
		return domain.NewError(domain.Invalid, "text field too long")
	}
	if math.IsNaN(p.Price) || math.IsInf(p.Price, 0) || p.Price < 0 || p.InStock < 0 {
		return domain.NewError(domain.Invalid, "price and in_stock must be finite and non-negative")
	}
	if len(p.Attributes) > 100 || len(p.Images) > 20 {
		return domain.NewError(domain.Invalid, "too many attributes or images")
	}
	for key, value := range p.Attributes {
		if len(key) > 128 || len(value) > 1024 {
			return domain.NewError(domain.Invalid, "attribute too long")
		}
	}
	for _, image := range p.Images {
		if len(image) > 2048 {
			return domain.NewError(domain.Invalid, "image URL too long")
		}
	}
	return nil
}

func (s *ProductService) Create(ctx context.Context, req domain.CreateProduct) (domain.Product, error) {
	if err := checkContext(ctx); err != nil {
		return domain.Product{}, err
	}
	p := domain.Product{SKU: req.SKU, Title: req.Title, Description: req.Description, Price: req.Price,
		CategoryID: req.CategoryID, BrandID: req.BrandID, Attributes: req.Attributes, Images: req.Images, InStock: req.InStock}
	if err := validate(p); err != nil {
		return domain.Product{}, err
	}
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return domain.Product{}, fmt.Errorf("generate UUID: %w", err)
	}
	bytes[6], bytes[8] = (bytes[6]&0x0f)|0x40, (bytes[8]&0x3f)|0x80
	id := hex.EncodeToString(bytes[:])
	p.ID = id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
	p.Version, p.UpdatedAt = 1, time.Now().UTC()
	if p.Attributes == nil {
		p.Attributes = map[string]string{}
	}
	if p.Images == nil {
		p.Images = []string{}
	}
	return s.repo.Create(ctx, p)
}

func (s *ProductService) Update(ctx context.Context, id string, req domain.UpdateProduct) (domain.Product, error) {
	if err := checkContext(ctx); err != nil {
		return domain.Product{}, err
	}
	if req.Version < 1 || req.Version == math.MaxInt64 {
		return domain.Product{}, domain.NewError(domain.Invalid, "version must be a positive incrementable int64")
	}
	p, err := s.Get(ctx, id)
	if err != nil {
		return domain.Product{}, err
	}
	if p.Version != req.Version {
		return domain.Product{}, domain.NewError(domain.Conflict, "version conflict; re-read product")
	}
	c := req.Changes
	if c.SKU == nil && c.Title == nil && c.Description == nil && c.Price == nil && c.CategoryID == nil && c.BrandID == nil && c.Attributes == nil && c.Images == nil && c.InStock == nil {
		return domain.Product{}, domain.NewError(domain.Invalid, "at least one change is required")
	}
	if c.SKU != nil {
		p.SKU = *c.SKU
	}
	if c.Title != nil {
		p.Title = *c.Title
	}
	if c.Description != nil {
		p.Description = *c.Description
	}
	if c.Price != nil {
		p.Price = *c.Price
	}
	if c.CategoryID != nil {
		p.CategoryID = *c.CategoryID
	}
	if c.BrandID != nil {
		p.BrandID = *c.BrandID
	}
	if c.Attributes != nil {
		p.Attributes = *c.Attributes
	}
	if c.Images != nil {
		p.Images = *c.Images
	}
	if c.InStock != nil {
		p.InStock = *c.InStock
	}
	if err := validate(p); err != nil {
		return domain.Product{}, err
	}
	p.UpdatedAt = time.Now().UTC()
	return s.repo.Update(ctx, p, req.Version)
}
