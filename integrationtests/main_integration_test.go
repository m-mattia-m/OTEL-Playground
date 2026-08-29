//go:build integration

// Package integrationtests drives the application the way a caller does: over
// HTTP, against a real Postgres in a container. Nothing in here is faked, so a
// failure means the wiring between the API, the domain and the database is
// broken and not just a single unit of it.
//
//	go test -tags=integration ./integrationtests/...
package integrationtests

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"otel-playground/internal/config"
	"otel-playground/internal/domain"
	"otel-playground/internal/infrastructure/api/controller"
	"otel-playground/internal/infrastructure/repository"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	databaseImage = "postgres:18-alpine"
	databaseName  = "app"
	databaseUser  = "app"
	// Only ever reachable inside the container of this test run.
	databasePassword = "integration"
)

var (
	// TestRepository and TestService are the real ones, on the real database.
	TestRepository *repository.Repository
	TestService    *domain.Service

	// PublicURL and ManagementURL are the two APIs, on their own ports, the
	// same split the deployment uses.
	PublicURL     string
	ManagementURL string
)

func TestMain(m *testing.M) {
	code, err := run(m)
	if err != nil {
		// A panic here would hide the reason behind a stack trace nobody reads.
		println("integration setup failed:", err.Error())
		os.Exit(1)
	}
	os.Exit(code)
}

// run owns the whole setup and tear down, so every defer still runs when the
// setup fails half way through.
func run(m *testing.M) (int, error) {
	ctx := context.Background()

	if err := chdirToRepositoryRoot(); err != nil {
		return 0, err
	}

	container, err := startDatabase(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = testcontainers.TerminateContainer(container) }()

	if err := configureForContainer(ctx, container); err != nil {
		return 0, err
	}

	// The real repository, which also applies the migrations.
	TestRepository, err = repository.NewRepository(ctx)
	if err != nil {
		return 0, err
	}
	defer TestRepository.Close()

	TestService = domain.NewService(TestRepository)

	routers, err := controller.NewRouters(ctx, TestService)
	if err != nil {
		return 0, err
	}

	public := httptest.NewServer(routers.Public)
	defer public.Close()
	PublicURL = public.URL

	management := httptest.NewServer(routers.Management)
	defer management.Close()
	ManagementURL = management.URL

	return m.Run(), nil
}

// startDatabase brings up a Postgres and waits until it answers.
func startDatabase(ctx context.Context) (*postgres.PostgresContainer, error) {
	return postgres.Run(ctx, databaseImage,
		postgres.WithDatabase(databaseName),
		postgres.WithUsername(databaseUser),
		postgres.WithPassword(databasePassword),
		postgres.BasicWaitStrategies(),
	)
}

// configureForContainer points the configuration at the container. It goes
// through the env vars on purpose, because that is the layer a deployment uses
// as well.
func configureForContainer(ctx context.Context, container *postgres.PostgresContainer) error {
	host, err := container.Host(ctx)
	if err != nil {
		return err
	}

	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return err
	}

	for key, value := range map[string]string{
		"APP_ENV":           "development",
		"LOGGING_LEVEL":     "error",
		"DATABASE_HOST":     host,
		"DATABASE_PORT":     port.Port(),
		"DATABASE_NAME":     databaseName,
		"DATABASE_USER":     databaseUser,
		"DATABASE_PASSWORD": databasePassword,
		"DATABASE_SSL_MODE": "disable",
		// The token verification has its own tests; these ones are about the
		// database and the HTTP surface.
		"AUTHENTICATION_SKIP": "true",
	} {
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}

	return config.Load()
}

// chdirToRepositoryRoot is needed because config.Load() reads
// 'config.default.yaml' out of the working directory.
func chdirToRepositoryRoot() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return os.Chdir(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return os.ErrNotExist
		}
		dir = parent
	}
}

// doRequest calls one of the two APIs and never follows a redirect, so a test
// can look at the redirect itself.
func doRequest(t *testing.T, method, baseURL, path string, body io.Reader, header map[string]string) *http.Response {
	t.Helper()

	request, err := http.NewRequest(method, baseURL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	for key, value := range header {
		request.Header.Set(key, value)
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}

	return response
}

// readBody reads and closes the body in one place, so no test has to.
func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("failed to close the response body: %v", err)
		}
	}()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
