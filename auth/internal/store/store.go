package store

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound          = errors.New("key not found")
	ErrRevoked           = errors.New("key is revoked")
	ErrComponentExists   = errors.New("component already exists")
	ErrComponentNotFound = errors.New("component not found")
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

// Component represents a provisioned LTS component.
type Component struct {
	Name             string    `json:"name"`
	Visibility       string    `json:"visibility"`
	RPMSeries        []string  `json:"rpm_series"`
	RPMOSFamilies    []string  `json:"rpm_os_families"`
	RPMArchitectures []string  `json:"rpm_architectures"`
	CreatedAt        time.Time `json:"created_at"`
}

// ComponentStore is the interface for component provisioning storage.
type ComponentStore interface {
	CreateComponent(ctx context.Context, comp *Component) (*Component, error)
	GetComponent(ctx context.Context, name string) (*Component, error)
	ListComponents(ctx context.Context) ([]*Component, error)
	DeleteComponent(ctx context.Context, name string) error
	RevokeComponentKeys(ctx context.Context, component string) (int64, error)
	CountActiveComponentKeys(ctx context.Context, component string) (int64, error)
	DeleteComponentWithRevoke(ctx context.Context, name string) (int64, error)
	UpdateComponentVisibility(ctx context.Context, name, visibility string) (*Component, error)
}
