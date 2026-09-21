// Package devices owns customer-managed device registrations for portal checkout.
package devices

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/netcore-isp/netcore/internal/security"
)

var (
	ErrInvalidRegistration = errors.New("devices: invalid registration")
	ErrDuplicateMAC        = errors.New("devices: MAC address is already registered")
	ErrUnavailable         = errors.New("devices: unavailable")
)

type Registration struct{ NormalizedMAC, Label string }
type Device struct {
	ID            string    `json:"id"`
	NormalizedMAC string    `json:"normalized_mac"`
	Label         string    `json:"label,omitempty"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

func NewRegistration(mac, label string) (Registration, error) {
	normalized, ok := security.NormalizeMAC(mac)
	label = strings.TrimSpace(label)
	if !ok || len(label) > 120 || strings.IndexFunc(label, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return Registration{}, ErrInvalidRegistration
	}
	return Registration{NormalizedMAC: normalized, Label: label}, nil
}

type Store interface {
	List(context.Context, string, string) ([]Device, error)
	Register(context.Context, string, string, Registration) (Device, error)
	RegisterForCustomer(context.Context, string, string, string, Registration) (Device, error)
	ListForCustomer(context.Context, string, string) ([]Device, error)
}

func (s *Service) ListForCustomer(ctx context.Context, tenantID, customerID string) ([]Device, error) {
	if s == nil || s.store == nil || !validUUID(tenantID) || !validUUID(customerID) {
		return nil, ErrUnavailable
	}
	return s.store.ListForCustomer(ctx, tenantID, customerID)
}

func (s *Service) RegisterForCustomer(ctx context.Context, tenantID, actorID, customerID, mac, label string) (Device, error) {
	if s == nil || s.store == nil || !validUUID(tenantID) || !validUUID(actorID) || !validUUID(customerID) {
		return Device{}, ErrUnavailable
	}
	input, err := NewRegistration(mac, label)
	if err != nil {
		return Device{}, err
	}
	return s.store.RegisterForCustomer(ctx, tenantID, actorID, customerID, input)
}

type Service struct{ store Store }

func NewService(store Store) (*Service, error) {
	if store == nil {
		return nil, errors.New("devices: store is required")
	}
	return &Service{store: store}, nil
}
func (s *Service) List(ctx context.Context, tenantID, userID string) ([]Device, error) {
	if s == nil || s.store == nil || !validUUID(tenantID) || !validUUID(userID) {
		return nil, ErrUnavailable
	}
	return s.store.List(ctx, tenantID, userID)
}
func (s *Service) Register(ctx context.Context, tenantID, userID, mac, label string) (Device, error) {
	if s == nil || s.store == nil || !validUUID(tenantID) || !validUUID(userID) {
		return Device{}, ErrUnavailable
	}
	input, err := NewRegistration(mac, label)
	if err != nil {
		return Device{}, err
	}
	return s.store.Register(ctx, tenantID, userID, input)
}
func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if value[i] != '-' {
				return false
			}
			continue
		}
		if !((value[i] >= '0' && value[i] <= '9') || (value[i] >= 'a' && value[i] <= 'f') || (value[i] >= 'A' && value[i] <= 'F')) {
			return false
		}
	}
	return true
}
