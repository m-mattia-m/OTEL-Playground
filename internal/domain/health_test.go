package domain

import (
	"context"
	"errors"
	"testing"

	"otel-playground/internal/infrastructure/repository"

	"github.com/stretchr/testify/require"
)

// fakeHealthRepository stands in for the database, so this test needs none.
type fakeHealthRepository struct {
	err    error
	called int
}

func (f *fakeHealthRepository) Check(context.Context) error {
	f.called++
	return f.err
}

func newServiceWith(repo repository.HealthRepository) *Service {
	service := &Service{}
	service.HealthService = NewHealthService(&repository.Repository{Health: repo}, service)
	return service
}

func TestReadinessReportsEveryDependency(t *testing.T) {
	t.Run("database reachable", func(t *testing.T) {
		repo := &fakeHealthRepository{}
		readiness := newServiceWith(repo).HealthService.Readiness(context.Background())

		require.True(t, readiness.Database)
		require.True(t, readiness.Ready())
		require.Equal(t, 1, repo.called, "the dependency has to be asked")
	})

	t.Run("database down", func(t *testing.T) {
		repo := &fakeHealthRepository{err: errors.New("dial tcp 10.0.3.4:5432: connection refused")}
		readiness := newServiceWith(repo).HealthService.Readiness(context.Background())

		require.False(t, readiness.Database)
		require.False(t, readiness.Ready(), "one broken dependency makes the application not ready")
	})
}

// The reason a dependency failed describes the inside of the deployment, so it
// must not leave the service; only the state does.
func TestReadinessDoesNotReturnTheReason(t *testing.T) {
	repo := &fakeHealthRepository{err: errors.New("password authentication failed for user \"app\"")}
	readiness := newServiceWith(repo).HealthService.Readiness(context.Background())

	require.False(t, readiness.Ready())
	// Readiness carries booleans only; there is no field a reason could hide in.
	require.Equal(t, Readiness{Database: false}, readiness)
}
