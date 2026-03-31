package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound = errors.New("key not found")
	ErrRevoked  = errors.New("key is revoked")
)

// Key represents a subscription key.
type Key struct {
	ID         string     `json:"id"`
	Component  string     `json:"component"`
	Active     bool       `json:"active"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	UsageCount int64      `json:"usage_count"`
}

// KeyStore is the interface for subscription key storage.
// All handler code interacts with this interface, never with a concrete implementation.
type KeyStore interface {
	CreateKey(ctx context.Context, component, label string, expiresAt *time.Time) (*Key, error)
	GetByValue(ctx context.Context, value string) (*Key, error)
	GetByID(ctx context.Context, id string) (*Key, error)
	ListKeys(ctx context.Context, component string) ([]*Key, error)
	RevokeKey(ctx context.Context, id string) error
	IncrementUsage(ctx context.Context, id string) error
}
