// Package auth provides API key authentication and tenant context propagation.
package auth

import (
	"context"
)

// Tenant represents an authenticated API client.
type Tenant struct {
	ID string
}

type tenantKey struct{}

// ContextWithTenant stores tenant info in the request context.
func ContextWithTenant(ctx context.Context, t Tenant) context.Context {
	return context.WithValue(ctx, tenantKey{}, t)
}

// TenantFromContext extracts the authenticated tenant from context.
// Returns false if no tenant was set (e.g. auth disabled).
func TenantFromContext(ctx context.Context) (Tenant, bool) {
	t, ok := ctx.Value(tenantKey{}).(Tenant)
	return t, ok
}

// KeyEntry defines a mapping from API key to tenant ID.
type KeyEntry struct {
	Key    string
	Tenant string
}

// Keystore validates API keys and resolves them to tenants.
type Keystore struct {
	keys map[string]Tenant
}

// NewKeystore creates a Keystore from a list of key entries.
func NewKeystore(entries []KeyEntry) *Keystore {
	keys := make(map[string]Tenant, len(entries))
	for _, e := range entries {
		keys[e.Key] = Tenant{ID: e.Tenant}
	}
	return &Keystore{keys: keys}
}

// Validate checks if the provided key is valid and returns the associated tenant.
func (ks *Keystore) Validate(key string) (Tenant, bool) {
	t, ok := ks.keys[key]
	return t, ok
}

// Len returns the number of registered keys.
func (ks *Keystore) Len() int {
	return len(ks.keys)
}
