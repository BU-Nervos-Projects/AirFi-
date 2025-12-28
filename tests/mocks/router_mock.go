package mocks

import (
	"context"
	"fmt"
	"sync"
)

// MockRouter is a mock implementation of the WiFi router interface.
type MockRouter struct {
	authorizedMACs map[string]bool
	mu             sync.RWMutex
	AuthorizeFunc  func(ctx context.Context, mac, ip, comment string) error
	DeauthFunc     func(ctx context.Context, mac string) error
}

// NewMockRouter creates a new mock router.
func NewMockRouter() *MockRouter {
	return &MockRouter{
		authorizedMACs: make(map[string]bool),
	}
}

// AuthorizeMAC authorizes a MAC address for network access.
func (m *MockRouter) AuthorizeMAC(ctx context.Context, mac, ip, comment string) error {
	if m.AuthorizeFunc != nil {
		return m.AuthorizeFunc(ctx, mac, ip, comment)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.authorizedMACs[mac] = true
	return nil
}

// DeauthorizeMAC removes network access for a MAC address.
func (m *MockRouter) DeauthorizeMAC(ctx context.Context, mac string) error {
	if m.DeauthFunc != nil {
		return m.DeauthFunc(ctx, mac)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.authorizedMACs, mac)
	return nil
}

// IsAuthorized checks if a MAC address is authorized.
func (m *MockRouter) IsAuthorized(mac string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.authorizedMACs[mac]
}

// GetAuthorizedCount returns the number of authorized MACs.
func (m *MockRouter) GetAuthorizedCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.authorizedMACs)
}

// Reset clears all authorized MACs.
func (m *MockRouter) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authorizedMACs = make(map[string]bool)
}

// MockRouterWithError is a mock router that returns errors.
type MockRouterWithError struct {
	*MockRouter
	AuthorizeError   error
	DeauthorizeError error
}

// NewMockRouterWithError creates a mock router that returns specified errors.
func NewMockRouterWithError(authErr, deauthErr error) *MockRouterWithError {
	return &MockRouterWithError{
		MockRouter:       NewMockRouter(),
		AuthorizeError:   authErr,
		DeauthorizeError: deauthErr,
	}
}

// AuthorizeMAC returns the configured error.
func (m *MockRouterWithError) AuthorizeMAC(ctx context.Context, mac, ip, comment string) error {
	if m.AuthorizeError != nil {
		return m.AuthorizeError
	}
	return m.MockRouter.AuthorizeMAC(ctx, mac, ip, comment)
}

// DeauthorizeMAC returns the configured error.
func (m *MockRouterWithError) DeauthorizeMAC(ctx context.Context, mac string) error {
	if m.DeauthorizeError != nil {
		return m.DeauthorizeError
	}
	return m.MockRouter.DeauthorizeMAC(ctx, mac)
}

// MockRouterWithTracking tracks all calls for verification.
type MockRouterWithTracking struct {
	*MockRouter
	AuthorizeCalls   []AuthorizeCall
	DeauthorizeCalls []string
	mu               sync.Mutex
}

// AuthorizeCall represents a call to AuthorizeMAC.
type AuthorizeCall struct {
	MAC     string
	IP      string
	Comment string
}

// NewMockRouterWithTracking creates a mock router that tracks calls.
func NewMockRouterWithTracking() *MockRouterWithTracking {
	return &MockRouterWithTracking{
		MockRouter:       NewMockRouter(),
		AuthorizeCalls:   make([]AuthorizeCall, 0),
		DeauthorizeCalls: make([]string, 0),
	}
}

// AuthorizeMAC tracks the call and delegates to MockRouter.
func (m *MockRouterWithTracking) AuthorizeMAC(ctx context.Context, mac, ip, comment string) error {
	m.mu.Lock()
	m.AuthorizeCalls = append(m.AuthorizeCalls, AuthorizeCall{
		MAC:     mac,
		IP:      ip,
		Comment: comment,
	})
	m.mu.Unlock()
	return m.MockRouter.AuthorizeMAC(ctx, mac, ip, comment)
}

// DeauthorizeMAC tracks the call and delegates to MockRouter.
func (m *MockRouterWithTracking) DeauthorizeMAC(ctx context.Context, mac string) error {
	m.mu.Lock()
	m.DeauthorizeCalls = append(m.DeauthorizeCalls, mac)
	m.mu.Unlock()
	return m.MockRouter.DeauthorizeMAC(ctx, mac)
}

// GetAuthorizeCalls returns all authorize calls.
func (m *MockRouterWithTracking) GetAuthorizeCalls() []AuthorizeCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AuthorizeCall{}, m.AuthorizeCalls...)
}

// GetDeauthorizeCalls returns all deauthorize calls.
func (m *MockRouterWithTracking) GetDeauthorizeCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string{}, m.DeauthorizeCalls...)
}

// SimulatedLatencyRouter adds simulated latency to operations.
type SimulatedLatencyRouter struct {
	*MockRouter
	LatencyMs int
}

// NewSimulatedLatencyRouter creates a mock router with simulated latency.
func NewSimulatedLatencyRouter(latencyMs int) *SimulatedLatencyRouter {
	return &SimulatedLatencyRouter{
		MockRouter: NewMockRouter(),
		LatencyMs:  latencyMs,
	}
}

// ValidateMACAddress validates MAC address format.
func ValidateMACAddress(mac string) error {
	if len(mac) != 17 {
		return fmt.Errorf("invalid MAC address length: %d", len(mac))
	}
	// Simple validation - check for colons
	colonCount := 0
	for _, c := range mac {
		if c == ':' {
			colonCount++
		}
	}
	if colonCount != 5 {
		return fmt.Errorf("invalid MAC address format")
	}
	return nil
}
