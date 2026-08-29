package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"otel-playground/internal/config"
	"otel-playground/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestMain moves to the repository root once, because config.Load() reads
// 'config.default.yaml' out of the working directory.
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)

	dir, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("go.mod not found")
		}
		dir = parent
	}
	if err := os.Chdir(dir); err != nil {
		panic(err)
	}

	os.Exit(m.Run())
}

// fakeHealthService replaces the domain, so none of these tests needs a
// database or a repository.
type fakeHealthService struct{ ready bool }

func (f fakeHealthService) Readiness(context.Context) domain.Readiness {
	return domain.Readiness{Database: f.ready}
}

func serviceWith(ready bool) *domain.Service {
	return &domain.Service{HealthService: fakeHealthService{ready: ready}}
}

// get drives the engine in process, without opening a port.
func get(t *testing.T, engine *gin.Engine, path string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, path, nil)
	for key, value := range header {
		request.Header.Set(key, value)
	}

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func TestReadinessProbe(t *testing.T) {
	t.Setenv("AUTHENTICATION_SKIP", "true")
	t.Setenv("APP_ENV", "development")
	require.NoError(t, config.Load())

	t.Run("every dependency usable", func(t *testing.T) {
		engine, err := newManagementRouter(serviceWith(true))
		require.NoError(t, err)

		response := get(t, engine, "/health/readiness", nil)
		require.Equal(t, http.StatusOK, response.Code)
		require.Contains(t, response.Body.String(), `"status":"ready"`)
		require.Contains(t, response.Body.String(), `"database":"ready"`)
	})

	t.Run("database down answers with a 503", func(t *testing.T) {
		engine, err := newManagementRouter(serviceWith(false))
		require.NoError(t, err)

		response := get(t, engine, "/health/readiness", nil)
		// The platform only reads the status code, so the body alone would keep
		// the traffic on this instance.
		require.Equal(t, http.StatusServiceUnavailable, response.Code)
		require.Contains(t, response.Body.String(), `"status":"not_ready"`)
		require.Contains(t, response.Body.String(), `"database":"not_ready"`)
	})
}

// A failing dependency must not restart the process, so the liveness probe must
// not look at one.
func TestLivenessProbeIgnoresTheDependencies(t *testing.T) {
	t.Setenv("AUTHENTICATION_SKIP", "true")
	t.Setenv("APP_ENV", "development")
	require.NoError(t, config.Load())

	engine, err := newManagementRouter(serviceWith(false))
	require.NoError(t, err)

	response := get(t, engine, "/health/liveness", nil)
	require.Equal(t, http.StatusOK, response.Code,
		"a broken database must not make the process look dead")
	require.Contains(t, response.Body.String(), `"status":"alive"`)
}

func TestRootRedirect(t *testing.T) {
	for _, tc := range []struct{ configured, want string }{
		{"", "/docs"},
		{"/docs", "/docs"},
		{"/openapi.json", "/openapi.json"},
		{"https://google.com", "https://google.com"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			t.Setenv("AUTHENTICATION_SKIP", "true")
			t.Setenv("APP_ENV", "development")
			t.Setenv("APP_ROOT_REDIRECT", tc.configured)
			require.NoError(t, config.Load())

			engine, err := newPublicRouter(context.Background())
			require.NoError(t, err)

			response := get(t, engine, "/", nil)
			require.Equal(t, http.StatusFound, response.Code)
			require.Equal(t, tc.want, response.Header().Get("Location"))
		})
	}
}

// Only the listed proxies may set the headers the client IP is taken from.
func TestTrustedProxiesAreApplied(t *testing.T) {
	t.Setenv("AUTHENTICATION_SKIP", "true")
	t.Setenv("APP_ENV", "development")

	t.Run("a valid list is accepted", func(t *testing.T) {
		t.Setenv("API_TRUSTED_PROXIES", "10.0.0.1,10.0.0.2")
		require.NoError(t, config.Load())
		_, err := newEngine()
		require.NoError(t, err)
	})

	t.Run("an invalid address stops the startup", func(t *testing.T) {
		t.Setenv("API_TRUSTED_PROXIES", "not-an-ip")
		require.NoError(t, config.Load())
		_, err := newEngine()
		require.Error(t, err)
		require.Contains(t, err.Error(), "trusted proxies")
	})
}

// The documentation and the entrypoint stay reachable, they declare no security
// scheme.
func TestUnprotectedEndpointsStayOpen(t *testing.T) {
	t.Setenv("AUTHENTICATION_SKIP", "true")
	t.Setenv("APP_ENV", "development")
	require.NoError(t, config.Load())

	engine, err := newPublicRouter(context.Background())
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, get(t, engine, "/docs", nil).Code)
	require.Equal(t, http.StatusOK, get(t, engine, "/openapi.json", nil).Code)
}
