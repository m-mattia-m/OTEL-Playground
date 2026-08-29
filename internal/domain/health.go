package domain

import (
	"context"
	"otel-playground/internal/infrastructure/repository"
	"otel-playground/utils/telemetry"
	"otel-playground/utils/zaphelper"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
)

// HealthService answers whether the application is able to serve traffic. It
// knows which dependencies are needed for that; the API layer only turns the
// answer into a response.
type HealthService interface {
	// Readiness reports the state of every dependency. It answers instead of
	// failing, because a broken dependency is a state to report and not an
	// error of the call itself.
	Readiness(ctx context.Context) Readiness
}

// Readiness is the state of every dependency the application needs.
type Readiness struct {
	Database bool
}

// Ready is true as long as every dependency is usable. Deriving it here means
// that a dependency which gets added to Readiness cannot be forgotten in the
// overall answer.
func (r Readiness) Ready() bool {
	return r.Database
}

type healthServiceImpl struct {
	Repository *repository.Repository
	Domain     *Service
}

func NewHealthService(repository *repository.Repository, domain *Service) HealthService {
	return &healthServiceImpl{
		Repository: repository,
		Domain:     domain,
	}
}

// Readiness checks every dependency. It does not stop at the first broken one,
// so the answer names all of them instead of only the one which failed first.
func (s *healthServiceImpl) Readiness(ctx context.Context) Readiness {
	ctx, span := telemetry.Tracer().Start(ctx, "HealthService.Readiness")
	defer span.End()

	readiness := Readiness{
		Database: true,
	}

	if err := s.Repository.Health.Check(ctx); err != nil {
		// The reason describes the inside of the deployment, so it only belongs
		// into the log. What leaves this method is the state alone.
		zap.L().Warn("Readiness check failed",
			zap.String("dependency", "database"),
			zap.Error(err),
			zaphelper.TraceID(ctx),
		)
		readiness.Database = false
	}

	// The state of every dependency belongs on the span as well, so a trace
	// alone already shows which one kept the instance out of the load balancer.
	span.SetAttributes(attribute.Bool("readiness.database", readiness.Database))
	if !readiness.Ready() {
		span.SetStatus(codes.Error, "not ready")
	}

	return readiness
}
