// Package grpc adapts generated protobuf messages to domain use cases.
package grpc

import (
	"context"
	"errors"
	"log/slog"

	pb "example.com/productcatalog/gen/product/v1"
	"example.com/productcatalog/internal/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Service interface {
	Get(context.Context, string) (domain.Product, error)
	List(context.Context, string, string, int) (domain.Page, error)
	Create(context.Context, domain.CreateProduct) (domain.Product, error)
	Update(context.Context, string, domain.UpdateProduct) (domain.Product, error)
}

type Handler struct {
	pb.UnimplementedProductServiceServer
	service Service
}

func NewHandler(service Service) *Handler { return &Handler{service: service} }

func ToProto(p domain.Product) *pb.Product {
	return &pb.Product{Id: p.ID, Sku: p.SKU, Title: p.Title, Description: p.Description, Price: p.Price,
		CategoryId: p.CategoryID, BrandId: p.BrandID, Attributes: p.Attributes, Images: p.Images,
		InStock: p.InStock, Version: p.Version, UpdatedAt: timestamppb.New(p.UpdatedAt)}
}

func (h *Handler) GetProduct(ctx context.Context, req *pb.GetProductRequest) (*pb.Product, error) {
	p, err := h.service.Get(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return ToProto(p), nil
}

func (h *Handler) ListCategoryProducts(ctx context.Context, req *pb.ListCategoryProductsRequest) (*pb.ListCategoryProductsResponse, error) {
	page, err := h.service.List(ctx, req.GetCategoryId(), req.GetCursor(), int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	result := &pb.ListCategoryProductsResponse{Products: make([]*pb.Product, 0, len(page.Products)), NextCursor: page.NextCursor}
	for _, p := range page.Products {
		result.Products = append(result.Products, ToProto(p))
	}
	return result, nil
}

func (h *Handler) CreateProduct(ctx context.Context, req *pb.CreateProductRequest) (*pb.Product, error) {
	p, err := h.service.Create(ctx, domain.CreateProduct{SKU: req.GetSku(), Title: req.GetTitle(), Description: req.GetDescription(),
		Price: req.GetPrice(), CategoryID: req.GetCategoryId(), BrandID: req.GetBrandId(), Attributes: req.GetAttributes(), Images: req.GetImages(), InStock: req.GetInStock()})
	if err != nil {
		return nil, err
	}
	return ToProto(p), nil
}

func (h *Handler) UpdateProduct(ctx context.Context, req *pb.UpdateProductRequest) (*pb.Product, error) {
	c := req.GetChanges()
	if c == nil {
		return nil, domain.NewError(domain.Invalid, "changes is required")
	}
	changes := domain.Changes{SKU: c.Sku, Title: c.Title, Description: c.Description, Price: c.Price,
		CategoryID: c.CategoryId, BrandID: c.BrandId, InStock: c.InStock}
	if c.Attributes != nil {
		values := c.Attributes.GetValues()
		if values == nil {
			values = map[string]string{}
		}
		changes.Attributes = &values
	}
	if c.Images != nil {
		values := c.Images.GetValues()
		if values == nil {
			values = []string{}
		}
		changes.Images = &values
	}
	p, err := h.service.Update(ctx, req.GetId(), domain.UpdateProduct{Version: req.GetVersion(), Changes: changes})
	if err != nil {
		return nil, err
	}
	return ToProto(p), nil
}

func toStatus(err error, logger *slog.Logger) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return status.FromContextError(err).Err()
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	switch domain.KindOf(err) {
	case domain.Invalid:
		return status.Error(codes.InvalidArgument, err.Error())
	case domain.NotFound:
		return status.Error(codes.NotFound, err.Error())
	case domain.Conflict:
		return status.Error(codes.Aborted, err.Error())
	case domain.Overloaded:
		return status.Error(codes.Unavailable, err.Error())
	default:
		logger.Error("RPC failed", "error", err)
		return status.Error(codes.Internal, "internal server error")
	}
}
