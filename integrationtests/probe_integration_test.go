//go:build integration

package integrationtests

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"otel-playground/internal/config"
	"otel-playground/internal/infrastructure/repository"

	"github.com/stretchr/testify/require"
)

// The readiness probe reaches through the domain and the repository into the
// database, so a 200 here means the whole chain works.
func Test_Probe_Readiness(t *testing.T) {
	response := doRequest(t, http.MethodGet, ManagementURL, "/health/readiness", nil, nil)
	body := readBody(t, response)

	require.Equal(t, http.StatusOK, response.StatusCode)

	var readiness struct {
		Status   string `json:"status"`
		Database string `json:"database"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &readiness))
	require.Equal(t, "ready", readiness.Status)
	require.Equal(t, "ready", readiness.Database, "the real database has to be reported as usable")
}

func Test_Probe_Liveness(t *testing.T) {
	response := doRequest(t, http.MethodGet, ManagementURL, "/health/liveness", nil, nil)
	body := readBody(t, response)

	require.Equal(t, http.StatusOK, response.StatusCode)

	var liveness struct {
		Status string `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &liveness))
	require.Equal(t, "alive", liveness.Status)
}

// The probes belong to the management API only. On the public port they must
// not exist, otherwise the split would be pointless.
func Test_Probe_NotOnThePublicAPI(t *testing.T) {
	for _, path := range []string{"/health/readiness", "/health/liveness"} {
		response := doRequest(t, http.MethodGet, PublicURL, path, nil, nil)
		_ = readBody(t, response)
		require.Equal(t, http.StatusNotFound, response.StatusCode, "%s must not be public", path)
	}
}

// The repository applies the migrations on startup, so the table which tracks
// them has to exist by now.
func Test_Repository_MigrationsWereApplied(t *testing.T) {
	require.NoError(t, TestRepository.Health.Check(context.Background()))

	response := doRequest(t, http.MethodGet, ManagementURL, "/health/readiness", nil, nil)
	_ = readBody(t, response)
	require.Equal(t, http.StatusOK, response.StatusCode)
}

// A database which cannot be reached has to stop the startup, instead of
// letting the first request find out.
func Test_Repository_UnreachableDatabaseFailsTheStartup(t *testing.T) {
	// Port 1 is reserved and never listening.
	t.Setenv("DATABASE_PORT", "1")
	t.Setenv("DATABASE_HOST", "127.0.0.1")
	require.NoError(t, config.Load())

	// Put the configuration back for every test which runs after this one.
	t.Cleanup(func() {
		require.NoError(t, config.Load())
	})

	repo, err := repository.NewRepository(context.Background())
	if repo != nil {
		repo.Close()
	}

	require.Error(t, err, "an unreachable database must not produce a usable repository")
	require.Contains(t, err.Error(), "database")
	// The error names the address so it can be fixed, but never the password.
	require.NotContains(t, err.Error(), os.Getenv("DATABASE_PASSWORD"))
}
