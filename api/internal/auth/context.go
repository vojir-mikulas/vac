package auth

import (
	"context"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

type ctxKey int

const (
	userKey ctxKey = iota
	sessionKey
)

// WithUser returns a child context carrying u as the authenticated user.
func WithUser(ctx context.Context, u *store.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// User returns the authenticated user attached by Auth middleware, or nil for
// anonymous requests.
func User(ctx context.Context) *store.User {
	u, _ := ctx.Value(userKey).(*store.User)
	return u
}

// WithSession attaches the session row alongside the user.
func WithSession(ctx context.Context, s *store.Session) context.Context {
	return context.WithValue(ctx, sessionKey, s)
}

// Session returns the active session, or nil for anonymous requests.
func Session(ctx context.Context) *store.Session {
	s, _ := ctx.Value(sessionKey).(*store.Session)
	return s
}
