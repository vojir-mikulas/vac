// Package store owns Postgres reads and writes. It is intentionally thin —
// no business logic, no transactions across multiple methods. Handlers and
// services compose the higher-level behaviour.
package store

import (
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a SELECT matches zero rows.
var ErrNotFound = errors.New("store: not found")

// ErrConflict is returned when an INSERT/UPDATE violates a UNIQUE constraint.
// Callers translate this to HTTP 409.
var ErrConflict = errors.New("store: conflict")

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}
