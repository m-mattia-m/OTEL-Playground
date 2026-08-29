package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
	envV2 "github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
	"go.uber.org/zap/zapcore"
)

// Configuration TODO: add validation if field is required and required validation in validateConfig() function
type Configuration struct {
	App struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		Env         string `yaml:"env"`
		Version     string `yaml:"version"`
		Port        int    `yaml:"port"`
		Contact     struct {
			Name string `yaml:"name"`
			Mail string `yaml:"mail"`
		} `yaml:"contact"`
		Root struct {
			// Redirect is where '/' points to; see ParseRootRedirect for the
			// accepted values.
			Redirect string `yaml:"redirect"`
		} `yaml:"root"`
	} `yaml:"app"`
	// Management is the internal API which holds the probes and the metrics. It
	// runs behind its own port and should not be exposed publicly.
	Management struct {
		Port    int `yaml:"port"`
		Metrics struct {
			Enabled bool   `yaml:"enabled"`
			Path    string `yaml:"path"`
		} `yaml:"metrics"`
		Probes struct {
			Readiness bool `yaml:"readiness"`
			Liveness  bool `yaml:"liveness"`
		} `yaml:"probes"`
	} `yaml:"management"`
	// Api holds the settings which apply to the API itself, independent of the
	// port it is served on.
	Api struct {
		// TrustedProxies are the proxies which are allowed to set the headers
		// gin derives the client IP from. As an env var the list is comma
		// separated, so it should be read with Strings(TrustedProxies).
		TrustedProxies []string `yaml:"trusted_proxies"`
	} `yaml:"api"`
	Database struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
		Name string `yaml:"name"`
		User string `yaml:"user"`
		// Password must not be in a tracked config file, so it is set with the
		// DATABASE_PASSWORD env var. The json tag keeps it out of every log
		// line which dumps the configuration.
		Password string `yaml:"password" json:"-"`
		// SslMode is passed to Postgres as it is; see ParseSSLMode for the
		// accepted values.
		SslMode string `yaml:"ssl_mode"`
		// MaxConnections is the upper limit of the connection pool.
		MaxConnections int `yaml:"max_connections"`
	} `yaml:"database"`
	Logging struct {
		// Level is the lowest level which still gets logged; see ParseLogLevel
		// for the accepted values.
		Level string `yaml:"level"`
	} `yaml:"logging"`
	Authentication struct {
		// Skip turns the verification of the tokens off. It exists for
		// development, where an issuer is not always at hand, and it is
		// refused in every other environment; see validateAuthentication().
		// It is an opt-out on purpose: a key which is missing, empty or
		// misspelled leaves the verification on instead of turning it off.
		Skip bool `yaml:"skip"`
		Oidc struct {
			Issuer   string `yaml:"issuer"`
			ClientId string `yaml:"clientId"`
			// Audience is the 'aud' claim a token has to carry. It falls back
			// to ClientId, which is what an ID token carries. An access token
			// is often audienced to the API instead, and then it has to be
			// named here.
			Audience string `yaml:"audience"`
		} `yaml:"oidc"`
	} `yaml:"authentication"`
	Otel struct {
		Traces struct {
			Exporter string `yaml:"exporter"`
		} `yaml:"traces"`
		Metrics struct {
			Exporter string `yaml:"exporter"`
		} `yaml:"metrics"`
		Logs struct {
			Exporter string `yaml:"exporter"`
		} `yaml:"logs"`
		Exporter struct {
			Otlp struct {
				Endpoint string `yaml:"endpoint"`
			} `yaml:"otlp"`
		} `yaml:"exporter"`
	} `yaml:"otel"`
}

var (
	k = koanf.New(".")
)

type Environment int

const (
	Development Environment = iota // dev, development, local
	Test                           // tst, test, qa
	Staging                        // stg, staging, preprod, pre, uat
	Production                     // prd, prod, production
)

// aliases maps every accepted spelling to its Environment.
var aliases = map[string]Environment{
	"dev": Development, "development": Development,
	"tst": Test, "test": Test,
	"stg": Staging, "staging": Staging,
	"prd": Production, "prod": Production, "production": Production,
}

const (
	// defaultConfigFile holds the base configuration. It is the only config
	// file that is tracked in git and therefore must not contain any
	// environment specific or secret value.
	defaultConfigFile = "config.default.yaml"
	// configFileEnvVar points to a second config file that overwrites the
	// base, e.g. CONFIGURATION_FILE_PATH=config.prod.yaml.
	configFileEnvVar = "CONFIGURATION_FILE_PATH"

	// EnvironmentKey, LogLevelKey, RootRedirectKey and DatabaseSSLModeKey only
	// accept a fixed set of values, which gets validated on startup; see
	// validateConfig().
	EnvironmentKey     = "app.env"
	LogLevelKey        = "logging.level"
	RootRedirectKey    = "app.root.redirect"
	DatabaseSSLModeKey = "database.ssl_mode"

	// AuthenticationSkipKey turns the verification of the tokens off. The keys
	// below it are only validated while it is on, because without a
	// verification they are not used at all.
	AuthenticationSkipKey = "authentication.skip"
	OidcIssuerKey         = "authentication.oidc.issuer"
	OidcClientIdKey       = "authentication.oidc.clientId"
	OidcAudienceKey       = "authentication.oidc.audience"

	// defaultSSLMode keeps a database without TLS reachable during local
	// development. Every deployment has to set 'database.ssl_mode' itself.
	defaultSSLMode = "disable"

	// defaultMaxConnections is the size of the connection pool if it is not
	// configured.
	defaultMaxConnections = 10
)

// Load builds the configuration out of three layers, where every layer only
// overwrites the single values it defines:
//
//	config.default.yaml -> $CONFIGURATION_FILE_PATH -> env vars
//
// Without CONFIGURATION_FILE_PATH only the base gets loaded.
func Load() error {
	// Load the base configuration.
	if err := k.Load(file.Provider(defaultConfigFile), yaml.Parser()); err != nil {
		return fmt.Errorf("load %s: %w", defaultConfigFile, err)
	}

	// Overwrite the base with the file the env var points to. A path that was
	// set explicitly has to exist, otherwise a typo would silently start the
	// application with the base configuration.
	if path := strings.TrimSpace(os.Getenv(configFileEnvVar)); path != "" {
		if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
			return fmt.Errorf("load %s=%q: %w", configFileEnvVar, path, err)
		}
	}

	// Overwrite config with env vars
	knownKeys := keysByEnvName()
	if err := k.Load(envV2.Provider(".", envV2.Opt{
		TransformFunc: func(name, value string) (string, any) {
			// An env var carries neither the case of a key nor its separator,
			// so API_TRUSTED_PROXIES has to be mapped back to the key the
			// config files use. Without it the env var would add
			// 'api.trusted.proxies' next to 'api.trusted_proxies' instead of
			// overwriting it.
			if key, ok := knownKeys[normalizeKey(name)]; ok {
				return key, value
			}
			return strings.ToLower(strings.ReplaceAll(name, "_", ".")), value
		},
	}), nil); err != nil {
		return err
	}

	// Validate if config is valid
	if err := validateConfig(); err != nil {
		return err
	}

	return nil
}

// keysByEnvName maps every key that was loaded from the config files to the
// name an env var would use, so 'api.trusted_proxies' is found under the same
// entry as API_TRUSTED_PROXIES. Keys which only exist as an env var are not
// part of it.
func keysByEnvName() map[string]string {
	keys := k.Keys()
	byEnvName := make(map[string]string, len(keys))
	for _, key := range keys {
		byEnvName[normalizeKey(key)] = key
	}
	return byEnvName
}

// keySeparators removes what tells a config key and an env var name apart.
var keySeparators = strings.NewReplacer(".", "", "_", "")

// normalizeKey reduces a key to what an env var can carry, so 'API_TRUSTED_PROXIES'
// and 'api.trusted_proxies' both end up as 'apitrustedproxies'.
func normalizeKey(key string) string {
	return keySeparators.Replace(strings.ToLower(key))
}

func validateConfig() error {
	// Validate if config can be mapped to the config structure
	var cfg Configuration
	if err := k.Unmarshal("", &cfg); err != nil {
		return err
	}

	// Both values decide how the application behaves, so an invalid one has to
	// stop the startup instead of silently falling back to a default.
	if _, err := ParseEnvironment(String(EnvironmentKey)); err != nil {
		return fmt.Errorf("invalid '%s': %w", EnvironmentKey, err)
	}

	if _, err := ParseLogLevel(String(LogLevelKey)); err != nil {
		return fmt.Errorf("invalid '%s': %w", LogLevelKey, err)
	}

	if _, err := ParseRootRedirect(String(RootRedirectKey)); err != nil {
		return fmt.Errorf("invalid '%s': %w", RootRedirectKey, err)
	}

	if _, err := ParseSSLMode(String(DatabaseSSLModeKey)); err != nil {
		return fmt.Errorf("invalid '%s': %w", DatabaseSSLModeKey, err)
	}

	if err := validateAuthentication(); err != nil {
		return err
	}

	return nil
}

func String(param string) string {
	return k.String(param)
}

// StringOr returns the configured value or the given fallback if the key was
// not set or empty.
func StringOr(param string, fallback string) string {
	value := strings.TrimSpace(k.String(param))
	if value == "" {
		return fallback
	}
	return value
}

func StringSanitized(param string) string {
	var slugRe = regexp.MustCompile(`[^a-z0-9]+`)
	var configValue = k.String(param)

	configValue = strings.ToLower(configValue)
	configValue = slugRe.ReplaceAllString(configValue, "-")
	return strings.Trim(configValue, "-")
}

// Strings returns a list value. In the config files a list is a YAML sequence,
// as an env var it is a single comma separated string, e.g.
// API_TRUSTEDPROXIES="127.0.0.1,0.0.0.0". Both spellings return the same list,
// with the blank entries removed.
func Strings(param string) []string {
	values := k.Strings(param)
	if len(values) == 0 {
		// koanf only reads a real sequence, so a value which came from an env
		// var has to be split by hand.
		values = []string{k.String(param)}
	}

	list := make([]string, 0, len(values))
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			if item = strings.TrimSpace(item); item != "" {
				list = append(list, item)
			}
		}
	}
	return list
}

// Int this function since it was never validated
func Int(param string) int {
	return k.Int(param)
}

// IntOr returns the configured value or the given fallback if the key was not set.
func IntOr(param string, fallback int) int {
	if !k.Exists(param) {
		return fallback
	}
	return k.Int(param)
}

// Bool this function since it was never validated
func Bool(param string) bool {
	return k.Bool(param)
}

// Float this function since it was never validated
func Float(param string) float64 {
	return k.Float64(param)
}

// Keys this function since it was never validated
func Keys() []string {
	return k.Keys()
}

// Get this function since it was never validated
func Get() Configuration {
	var cfg Configuration
	// error does not get handled since config gets validated on initial load; see validateConfig()
	_ = k.Unmarshal("", &cfg)
	return cfg
}

// Environment is a deployment stage.

// String returns the canonical name.
func (e Environment) String() string {
	switch e {
	case Development:
		return "development"
	case Test:
		return "test"
	case Staging:
		return "staging"
	case Production:
		return "production"
	default:
		return "unknown"
	}
}

// ParseEnvironment resolves a string (case-insensitive, trimmed) to an
// Environment. An unset value is the only one which falls back to a default,
// every other unknown value is an error.
func ParseEnvironment(s string) (Environment, error) {
	value := strings.ToLower(strings.TrimSpace(s))
	if value == "" {
		return Development, nil
	}

	environment, ok := aliases[value]
	if !ok {
		return Development, fmt.Errorf("unknown environment %q; Valid options were: %s", s, validOptions(aliases))
	}
	return environment, nil
}

// GetEnvironment reads 'app.env'. The value is validated on startup, so it can
// only be a valid one at this point; see validateConfig().
func GetEnvironment() Environment {
	environment, _ := ParseEnvironment(String(EnvironmentKey))
	return environment
}

// logLevels maps every accepted spelling to its zap level.
var logLevels = map[string]zapcore.Level{
	"debug": zapcore.DebugLevel,
	"info":  zapcore.InfoLevel,
	"warn":  zapcore.WarnLevel,
	"error": zapcore.ErrorLevel,
	"fatal": zapcore.FatalLevel,
}

// ParseLogLevel resolves a string (case-insensitive, trimmed) to a zap level.
// An unset value is the only one which falls back to a default, every other
// unknown value is an error.
func ParseLogLevel(s string) (zapcore.Level, error) {
	value := strings.ToLower(strings.TrimSpace(s))
	if value == "" {
		return zapcore.InfoLevel, nil
	}

	level, ok := logLevels[value]
	if !ok {
		return zapcore.InfoLevel, fmt.Errorf("unknown log level %q; Valid options were: %s", s, validOptions(logLevels))
	}
	return level, nil
}

// GetLogLevel reads 'logging.level'. The value is validated on startup, so it
// can only be a valid one at this point; see validateConfig().
func GetLogLevel() zapcore.Level {
	level, _ := ParseLogLevel(String(LogLevelKey))
	return level
}

// ParseRootRedirect resolves where '/' redirects to. It is either a path on
// this host, e.g. '/docs', or an absolute URL to another host, e.g.
// 'https://google.com'. An unset value is returned as it is, so the caller can
// decide on its own default.
func ParseRootRedirect(s string) (string, error) {
	value := strings.TrimSpace(s)
	if value == "" || strings.HasPrefix(value, "/") {
		return value, nil
	}

	target, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("%q is not a URL: %w", s, err)
	}

	// Without a scheme the browser resolves the value against this host, so
	// 'google.com' would end up as the path '/google.com'.
	if target.Scheme != "http" && target.Scheme != "https" {
		return "", fmt.Errorf("%q is neither a path like '/docs' nor an absolute URL like 'https://%s'", s, value)
	}

	return value, nil
}

// validateAuthentication checks what the token verification needs. Skipping it
// is only allowed while developing, so a configuration which turns it off by
// accident cannot reach an environment where it matters.
func validateAuthentication() error {
	if Bool(AuthenticationSkipKey) {
		if environment := GetEnvironment(); environment != Development {
			return fmt.Errorf("'%s' is on while '%s' is %q; the authentication may only be skipped in %q",
				AuthenticationSkipKey, EnvironmentKey, environment, Development)
		}
		return nil
	}

	issuer := strings.TrimSpace(String(OidcIssuerKey))
	if issuer == "" {
		return fmt.Errorf("'%s' is empty and '%s' is off", OidcIssuerKey, AuthenticationSkipKey)
	}

	// The issuer is asked for its configuration over HTTP and has to match the
	// 'iss' claim of the tokens exactly, so it needs the scheme as well.
	target, err := url.Parse(issuer)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return fmt.Errorf("invalid '%s': %q has to be the absolute URL of the issuer, e.g. 'https://%s'", OidcIssuerKey, issuer, issuer)
	}

	if strings.TrimSpace(String(OidcClientIdKey)) == "" {
		return fmt.Errorf("'%s' is empty and '%s' is off", OidcClientIdKey, AuthenticationSkipKey)
	}

	return nil
}

// OidcAudience is the 'aud' claim every token has to carry. Without an explicit
// one it is the client id, which is what an ID token of this client carries.
func OidcAudience() string {
	return StringOr(OidcAudienceKey, String(OidcClientIdKey))
}

// sslModes are the modes Postgres accepts, from no encryption at all up to a
// fully verified certificate.
var sslModes = map[string]struct{}{
	"disable":     {},
	"allow":       {},
	"prefer":      {},
	"require":     {},
	"verify-ca":   {},
	"verify-full": {},
}

// ParseSSLMode resolves how the connection to the database is encrypted. An
// unset value falls back to defaultSSLMode.
func ParseSSLMode(s string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(s))
	if value == "" {
		return defaultSSLMode, nil
	}

	if _, ok := sslModes[value]; !ok {
		return "", fmt.Errorf("unknown ssl mode %q; Valid options were: %s", s, validOptions(sslModes))
	}
	return value, nil
}

// DatabaseDSN builds the connection URL of the database. Every part is escaped
// by net/url, so a password which contains a '@' or a '/' cannot break the URL
// apart. The result carries the password, so it must never be logged.
func DatabaseDSN() string {
	// The mode is validated on startup, so it can only be a valid one here.
	sslMode, _ := ParseSSLMode(String(DatabaseSSLModeKey))

	query := url.Values{}
	query.Set("sslmode", sslMode)

	dsn := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(String("database.user"), String("database.password")),
		Host:     databaseHost(),
		Path:     "/" + String("database.name"),
		RawQuery: query.Encode(),
	}

	return dsn.String()
}

// DatabaseAddress names the database without the credentials, so it can be
// logged.
func DatabaseAddress() string {
	return databaseHost() + "/" + String("database.name")
}

// DatabaseMaxConnections is the upper limit of the connection pool.
func DatabaseMaxConnections() int {
	return IntOr("database.max_connections", defaultMaxConnections)
}

func databaseHost() string {
	return net.JoinHostPort(String("database.host"), strconv.Itoa(Int("database.port")))
}

// validOptions returns the accepted spellings of a value, sorted, so an error
// message can list them.
func validOptions[V any](values map[string]V) string {
	options := make([]string, 0, len(values))
	for option := range values {
		options = append(options, option)
	}
	slices.Sort(options)
	return strings.Join(options, ", ")
}
