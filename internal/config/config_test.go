package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMain moves to the repository root once, because Load() reads
// 'config.default.yaml' out of the working directory.
func TestMain(m *testing.M) {
	root, err := repositoryRoot()
	if err != nil {
		panic(err)
	}
	if err := os.Chdir(root); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// repositoryRoot walks up until it finds the go.mod, so the tests do not depend
// on how deep the package sits.
func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

// load applies the given env vars and reads the configuration with them. The
// vars are removed again afterwards, so one case cannot leak into the next.
func load(t *testing.T, env map[string]string) error {
	t.Helper()
	for key, value := range env {
		t.Setenv(key, value)
	}
	return Load()
}

func TestParseEnvironment(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    Environment
		wantErr bool
	}{
		{"development", Development, false},
		{"dev", Development, false},
		{"DEV", Development, false},
		{"  prod  ", Production, false},
		{"production", Production, false},
		{"stg", Staging, false},
		{"tst", Test, false},
		// Unset falls back, every other unknown value is an error.
		{"", Development, false},
		{"nonsense", Development, true},
		{"prodd", Development, true},
	} {
		got, err := ParseEnvironment(tc.in)
		if tc.wantErr {
			require.Error(t, err, "%q should be rejected", tc.in)
			continue
		}
		require.NoError(t, err, "%q should be accepted", tc.in)
		require.Equal(t, tc.want, got, "%q", tc.in)
	}
}

func TestParseLogLevel(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"debug", "debug", false},
		{"INFO", "info", false},
		{" warn ", "warn", false},
		{"error", "error", false},
		{"fatal", "fatal", false},
		{"", "info", false},
		{"ASDF", "", true},
		{"trace", "", true},
	} {
		got, err := ParseLogLevel(tc.in)
		if tc.wantErr {
			require.Error(t, err, "%q should be rejected", tc.in)
			continue
		}
		require.NoError(t, err, "%q should be accepted", tc.in)
		require.Equal(t, tc.want, got.String(), "%q", tc.in)
	}
}

func TestParseRootRedirect(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"/docs", "/docs", false},
		{"/openapi.json", "/openapi.json", false},
		{"https://google.com", "https://google.com", false},
		{"http://internal.host/path", "http://internal.host/path", false},
		{"", "", false},
		// Without a scheme the browser would resolve it against this host, so
		// it has to be refused instead of silently becoming '/google.com'.
		{"google.com", "", true},
		{"ftp://example.com", "", true},
	} {
		got, err := ParseRootRedirect(tc.in)
		if tc.wantErr {
			require.Error(t, err, "%q should be rejected", tc.in)
			continue
		}
		require.NoError(t, err, "%q should be accepted", tc.in)
		require.Equal(t, tc.want, got, "%q", tc.in)
	}
}

func TestParseSSLMode(t *testing.T) {
	for _, mode := range []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"} {
		got, err := ParseSSLMode(mode)
		require.NoError(t, err)
		require.Equal(t, mode, got)
	}

	got, err := ParseSSLMode("")
	require.NoError(t, err)
	require.Equal(t, defaultSSLMode, got)

	_, err = ParseSSLMode("banana")
	require.Error(t, err)
	require.Contains(t, err.Error(), "verify-full", "the error should list the valid options")
}

// Strings has to read a YAML sequence and the comma separated string an env var
// carries as the very same list.
func TestStrings(t *testing.T) {
	require.NoError(t, load(t, nil))
	require.Equal(t, []string{"127.0.0.1", "0.0.0.0"}, Strings("api.trusted_proxies"),
		"the list of the config file")

	for _, tc := range []struct {
		env  string
		want []string
	}{
		{"10.0.0.1,10.0.0.2", []string{"10.0.0.1", "10.0.0.2"}},
		{"10.0.0.1, 10.0.0.2 ,,", []string{"10.0.0.1", "10.0.0.2"}},
		{"192.168.1.2", []string{"192.168.1.2"}},
	} {
		require.NoError(t, load(t, map[string]string{"API_TRUSTED_PROXIES": tc.env}))
		require.Equal(t, tc.want, Strings("api.trusted_proxies"), "env %q", tc.env)
	}
}

// An env var carries neither the case of a key nor its separator, so it has to
// be mapped back onto the key the config file uses instead of adding a second
// one next to it.
func TestEnvOverwritesTheConfigFileKey(t *testing.T) {
	require.NoError(t, load(t, map[string]string{
		"API_TRUSTED_PROXIES":          "10.0.0.9",
		"AUTHENTICATION_OIDC_CLIENTID": "from-env",
	}))

	require.Equal(t, []string{"10.0.0.9"}, Strings("api.trusted_proxies"),
		"snake_case key")
	require.Equal(t, "from-env", String(OidcClientIdKey),
		"camelCase key")
}

// The password is escaped, so a special character in it cannot break the URL
// apart.
func TestDatabaseDSNEscapesThePassword(t *testing.T) {
	require.NoError(t, load(t, map[string]string{
		"DATABASE_PASSWORD": "p@ss/w:rd?#&",
		"DATABASE_HOST":     "db.internal",
		"DATABASE_PORT":     "6432",
		"DATABASE_NAME":     "app",
		"DATABASE_USER":     "app",
		"DATABASE_SSL_MODE": "verify-full",
	}))

	dsn := DatabaseDSN()
	require.Contains(t, dsn, "p%40ss%2Fw%3Ard%3F%23&", "the password has to be escaped")
	require.Contains(t, dsn, "db.internal:6432")
	require.Contains(t, dsn, "sslmode=verify-full")
	require.True(t, len(dsn) > 0 && dsn[:11] == "postgres://")

	// The address is what gets logged, so it must not carry the credentials.
	require.Equal(t, "db.internal:6432/app", DatabaseAddress())
	require.NotContains(t, DatabaseAddress(), "p@ss")
}

// An invalid value has to stop the startup instead of silently falling back.
func TestLoadRefusesInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"log level", map[string]string{"LOGGING_LEVEL": "ASDF"}, "logging.level"},
		{"environment", map[string]string{"APP_ENV": "nonsense"}, "app.env"},
		{"root redirect", map[string]string{"APP_ROOT_REDIRECT": "google.com"}, "app.root.redirect"},
		{"ssl mode", map[string]string{"DATABASE_SSL_MODE": "banana"}, "database.ssl_mode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := load(t, tc.env)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

// Skipping the authentication is only allowed while developing. Everything else
// has to refuse to start, so a configuration which turns it off by accident
// cannot reach an environment where it matters.
func TestAuthenticationSkipIsRefusedOutsideDevelopment(t *testing.T) {
	for _, environment := range []string{"development", "test", "staging", "production"} {
		t.Run(environment, func(t *testing.T) {
			err := load(t, map[string]string{
				"AUTHENTICATION_SKIP":          "true",
				"APP_ENV":                      environment,
				"AUTHENTICATION_OIDC_ISSUER":   "https://issuer.example.com",
				"AUTHENTICATION_OIDC_CLIENTID": "client",
			})

			if environment == "development" {
				require.NoError(t, err)
				require.True(t, Bool(AuthenticationSkipKey))
				return
			}

			require.Error(t, err, "%s must not start with the authentication skipped", environment)
			require.Contains(t, err.Error(), AuthenticationSkipKey)
		})
	}
}

// The skip is an opt-out, so a key which is missing or empty has to leave the
// verification on. This is the property which keeps a broken configuration from
// silently opening the API.
func TestAuthenticationFailsClosed(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"absent", ""},
		{"empty", ""},
		{"false", "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{
				"APP_ENV":                      "production",
				"AUTHENTICATION_OIDC_ISSUER":   "https://issuer.example.com",
				"AUTHENTICATION_OIDC_CLIENTID": "client",
			}
			if tc.name != "absent" {
				env["AUTHENTICATION_SKIP"] = tc.value
			}

			require.NoError(t, load(t, env))
			require.False(t, Bool(AuthenticationSkipKey),
				"%s must not skip the authentication", tc.name)
		})
	}
}

// While the verification is on, everything it needs has to be there.
func TestAuthenticationNeedsAnIssuerAndAClient(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			"issuer without a scheme",
			map[string]string{"AUTHENTICATION_OIDC_ISSUER": "zitadel.example.com"},
			"absolute URL",
		},
		{
			"empty issuer",
			map[string]string{"AUTHENTICATION_OIDC_ISSUER": " "},
			OidcIssuerKey,
		},
		{
			"empty client id",
			map[string]string{
				"AUTHENTICATION_OIDC_ISSUER":   "https://issuer.example.com",
				"AUTHENTICATION_OIDC_CLIENTID": "",
			},
			OidcClientIdKey,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"AUTHENTICATION_SKIP": "false", "APP_ENV": "development"}
			for k, v := range tc.env {
				env[k] = v
			}
			err := load(t, env)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}
