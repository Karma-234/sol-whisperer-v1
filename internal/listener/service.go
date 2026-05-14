package listener

import (
	"context"
	"errors"
)

type CreateInput struct {
	UserID            string
	Mint              string
	Symbol            string
	EnableAutoSnipe   bool
	BuyAmountSOL      float64
	SlippagePct       float64
	PriorityFeeMicros int64
	JitoTipSOL        float64
}

type Listener struct {
	ID              string
	UserID          string
	Mint            string
	Symbol          string
	AutoSnipeEnable bool
}

type Repository interface {
	CreateListener(ctx context.Context, in CreateInput) (Listener, error)
}

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Listener, error) {
	if in.UserID == "" {
		return Listener{}, errors.New("user id is required")
	}
	if in.Mint == "" {
		return Listener{}, errors.New("mint is required")
	}
	if in.EnableAutoSnipe {
		if in.BuyAmountSOL <= 0 {
			return Listener{}, errors.New("buy amount must be > 0 when auto-snipe enabled")
		}
		if in.SlippagePct <= 0 {
			return Listener{}, errors.New("slippage must be > 0 when auto-snipe enabled")
		}
	}
	return s.repo.CreateListener(ctx, in)
}
