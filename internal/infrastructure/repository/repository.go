// Package repository holds everything which talks to the database. Every
// repository is an interface, so a caller depends on the behaviour and not on
// Postgres, and a test can replace it with a fake without a database.
package repository

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"otel-playground/internal/config"
	"otel-playground/migrations"

	"github.com/exaring/otelpgx"
	"github.com/golang-migrate/migrate/v4"
	// The pgx driver applies the migrations. It registers itself under the
	// 'pgx5' scheme; see migrationDSN().
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
)

// migrationScheme is the scheme the golang-migrate pgx v5 driver registers
// itself under.
const migrationScheme = "pgx5"

// Repository bundles the repository of every entity. All of them share the same
// connection pool and every field is an interface, so a test can replace a
// single one with a fake.
type Repository struct {
	Health HealthRepository

	pool *pgxpool.Pool
}

// NewRepository opens the connection pool, applies the migrations and builds
// every repository on top of that pool. The caller has to Close() it.
func NewRepository(ctx context.Context) (*Repository, error) {
	dsn := config.DatabaseDSN()

	pool, err := connectToDatabase(ctx, dsn)
	if err != nil {
		return nil, err
	}

	if err := runMigrations(dsn); err != nil {
		pool.Close()
		return nil, err
	}

	return &Repository{
		Health: NewHealthRepository(pool),
		pool:   pool,
	}, nil
}

// Close returns every connection of the pool. It waits until the queries which
// are still running are done.
func (r *Repository) Close() {
	if r.pool != nil {
		r.pool.Close()
	}
}

// connectToDatabase builds the pool and proves that the database answers, so a
// wrong host or password fails the startup instead of the first request.
func connectToDatabase(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to read the database connection string: %w", err)
	}
	poolConfig.MaxConns = int32(config.DatabaseMaxConnections())

	// Every query gets its own span below the span of the request which caused
	// it, so a trace shows the statement instead of only the endpoint. The
	// arguments are left out on purpose: they carry the data of the caller.
	poolConfig.ConnConfig.Tracer = otelpgx.NewTracer(
		otelpgx.WithTracerProvider(otel.GetTracerProvider()),
		otelpgx.WithTrimSQLInSpanName(),
	)

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build the connection pool: %w", err)
	}

	// The pool opens its connections lazily, so without this the first
	// connection would only be attempted on the first query.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to reach the database on %s: %w", config.DatabaseAddress(), err)
	}

	zap.L().Info("Database connected", zap.String("address", config.DatabaseAddress()))
	return pool, nil
}

// runMigrations applies every migration which is embedded in the binary. It is
// safe to run on more than one instance at once, because the driver takes an
// advisory lock in the database first.
func runMigrations(dsn string) error {
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("failed to read the embedded migrations: %w", err)
	}

	migrationDSN, err := migrationDSN(dsn)
	if err != nil {
		return err
	}

	migrator, err := migrate.NewWithSourceInstance("iofs", source, migrationDSN)
	if err != nil {
		return fmt.Errorf("failed to prepare the migrations: %w", err)
	}
	defer migrator.Close()

	err = migrator.Up()
	if errors.Is(err, migrate.ErrNoChange) {
		zap.L().Info("Database schema is up to date")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to apply the migrations: %w", err)
	}

	zap.L().Info("Database migrations applied")
	return nil
}

// migrationDSN is the same connection as the pool uses, but golang-migrate
// picks its driver by the scheme of the URL.
func migrationDSN(dsn string) (string, error) {
	target, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("failed to read the database connection string: %w", err)
	}

	target.Scheme = migrationScheme
	return target.String(), nil
}
