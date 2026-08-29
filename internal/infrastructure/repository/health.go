package repository

import (
	"context"
	"fmt"
	"otel-playground/utils/telemetry"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/codes"
)

// HealthRepository reports whether the database can be used. It is what the
// readiness probe is built on.
type HealthRepository interface {
	// Check answers with nil if the database is usable. The caller decides how
	// long it may take by the context it passes in.
	Check(ctx context.Context) error
}

type healthRepository struct {
	pool *pgxpool.Pool
}

func NewHealthRepository(pool *pgxpool.Pool) HealthRepository {
	return &healthRepository{pool: pool}
}

// Check runs the cheapest statement there is. It takes a connection out of the
// pool and waits for the answer of the server, so it also notices a pool which
// only hands out broken connections.
func (r *healthRepository) Check(ctx context.Context) error {
	ctx, span := telemetry.Tracer().Start(ctx, "HealthRepository.Check")
	defer span.End()

	var alive int
	if err := r.pool.QueryRow(ctx, `SELECT 1`).Scan(&alive); err != nil {
		// The span carries the failure as well, so the trace alone already
		// shows why the readiness probe answered with a 503.
		span.RecordError(err)
		span.SetStatus(codes.Error, "database is not reachable")
		return fmt.Errorf("database is not reachable: %w", err)
	}

	return nil
}
