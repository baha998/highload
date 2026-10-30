// Package domain contains transport-independent catalog types.
package domain

import (
	"errors"
	"maps"
	"slices"
	"time"
)

type Product struct {
	ID          string            `json:"id"`
	SKU         string            `json:"sku"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Price       float64           `json:"price"`
	CategoryID  string            `json:"category_id"`
	BrandID     string            `json:"brand_id"`
	Attributes  map[string]string `json:"attributes"`
	Images      []string          `json:"images"`
	InStock     int32             `json:"in_stock"`
	Version     int64             `json:"version"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func (p Product) Clone() Product {
	p.Attributes = maps.Clone(p.Attributes)
	p.Images = slices.Clone(p.Images)
	return p
}

type Category struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
}

type Brand struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type CreateProduct struct {
	SKU         string            `json:"sku"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Price       float64           `json:"price"`
	CategoryID  string            `json:"category_id"`
	BrandID     string            `json:"brand_id"`
	Attributes  map[string]string `json:"attributes"`
	Images      []string          `json:"images"`
	InStock     int32             `json:"in_stock"`
}

// Pointers preserve field presence, including zero price and zero stock.
type Changes struct {
	SKU         *string            `json:"sku,omitempty"`
	Title       *string            `json:"title,omitempty"`
	Description *string            `json:"description,omitempty"`
	Price       *float64           `json:"price,omitempty"`
	CategoryID  *string            `json:"category_id,omitempty"`
	BrandID     *string            `json:"brand_id,omitempty"`
	Attributes  *map[string]string `json:"attributes,omitempty"`
	Images      *[]string          `json:"images,omitempty"`
	InStock     *int32             `json:"in_stock,omitempty"`
}

type UpdateProduct struct {
	Version int64 `json:"version"`
	Changes
}

type Page struct {
	Products   []Product `json:"products"`
	NextCursor string    `json:"next_cursor"`
}

// Each operation returns its own error; no mutable package-level sentinels.
type ErrorKind uint8

const (
	Invalid ErrorKind = iota + 1
	NotFound
	Conflict
	Overloaded
)

type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string { return e.Message }

func NewError(kind ErrorKind, message string) error {
	return &Error{Kind: kind, Message: message}
}

func KindOf(err error) ErrorKind {
	var target *Error
	if errors.As(err, &target) {
		return target.Kind
	}
	return 0
}
