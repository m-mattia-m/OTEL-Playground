package controller

import (
	"context"
	"fmt"
	"net/http"
	"otel-playground/internal/config"
	"otel-playground/internal/domain"
	"otel-playground/internal/infrastructure/api/response"
	"otel-playground/utils/zaphelper"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.uber.org/zap"
)

const (
	// openAPIPath serves the specification as '/openapi.json' and '/openapi.yaml'.
	openAPIPath = "/openapi"
	// docsPath serves the API documentation which Huma renders itself.
	docsPath = "/docs"
	// schemasPath serves the single JSON schemas of the API models.
	schemasPath = "/schemas"

	defaultPublicPort     = 8083
	defaultManagementPort = 8084

	// defaultRootRedirect points '/' at the rendered documentation as long as
	// 'app.root.redirect' does not name another target.
	defaultRootRedirect = docsPath

	// bearerScheme lets a caller paste a token into the documentation instead
	// of running the whole flow for it.
	bearerScheme = "bearer"
	// oauthScheme runs the authorization code flow with PKCE, so the
	// documentation fetches the token itself.
	oauthScheme = "oauth2"
)

// Routers holds the two engines of the application. The public one carries the
// business endpoints and the documentation, the management one carries
// everything which should not be exposed publicly, like the probes and the
// metrics.
type Routers struct {
	Public            *gin.Engine
	PublicAddress     string
	Management        *gin.Engine
	ManagementAddress string
}

// NewRouters builds the public and the management router. Both of them are
// Huma APIs, so every endpoint is part of an OpenAPI specification.
func NewRouters(ctx context.Context, service *domain.Service) (*Routers, error) {

	if config.GetEnvironment() == config.Production {
		gin.SetMode(gin.ReleaseMode)
	}

	public, err := newPublicRouter(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to build the public router: %w", err)
	}

	management, err := newManagementRouter(service)
	if err != nil {
		return nil, fmt.Errorf("failed to build the management router: %w", err)
	}

	return &Routers{
		Public:            public,
		PublicAddress:     fmt.Sprintf(":%d", config.IntOr("app.port", defaultPublicPort)),
		Management:        management,
		ManagementAddress: fmt.Sprintf(":%d", config.IntOr("management.port", defaultManagementPort)),
	}, nil
}

// newPublicRouter builds the publicly exposed API. It is the only one which
// verifies the tokens: the management API is not reachable from the outside and
// is called by the platform, which carries no token.
func newPublicRouter(ctx context.Context) (*gin.Engine, error) {
	engine, err := newEngine()
	if err != nil {
		return nil, err
	}

	api := humagin.New(engine, publicHumaConfig())

	if err := registerAuthentication(ctx, api); err != nil {
		return nil, err
	}

	registerRootRedirect(api)

	huma.Register(api, huma.Operation{
		Method:      http.MethodGet,
		OperationID: "get-ping",
		Summary:     "Ping",
		Description: "Answers with 'pong' to prove that the API is reachable. It needs a valid token, so it also proves that the authentication works.",
		Path:        "/ping",
		Tags:        []string{"Common"},
		Security:    authenticated(),
	}, func(ctx context.Context, _ *struct{}) (*response.PingResponse, error) {
		zap.L().Info("pong", zaphelper.TraceID(ctx))
		return &response.PingResponse{Body: response.Ping{Message: "pong"}}, nil
	})

	return engine, nil
}

// newEngine builds a gin engine with the instrumentation which every API of
// this application needs.
func newEngine() (*gin.Engine, error) {
	engine := gin.Default()
	engine.Use(otelgin.Middleware(config.StringSanitized("app.name")))

	// Only the configured proxies are allowed to set the headers gin derives
	// the client IP from, so a client cannot claim a foreign address with
	// 'X-Forwarded-For'. An empty list trusts none of them.
	trustedProxies := config.Strings("api.trusted_proxies")
	if err := engine.SetTrustedProxies(trustedProxies); err != nil {
		return nil, fmt.Errorf("failed to set the trusted proxies %v: %w", trustedProxies, err)
	}

	return engine, nil
}

// registerRootRedirect points '/' at the target which is configured with
// 'app.root.redirect'. Both APIs register the same one, so the entrypoint
// behaves the same no matter which port it is called on.
func registerRootRedirect(api huma.API) {
	// The value is validated on startup, so it is either a path on this host or
	// an absolute URL to another host; see config.ParseRootRedirect().
	rootRedirect := config.StringOr(config.RootRedirectKey, defaultRootRedirect)

	huma.Register(api, huma.Operation{
		Method:      http.MethodGet,
		OperationID: "get-root",
		Summary:     "Entrypoint",
		Description: fmt.Sprintf("Redirects to %q, which is configured with '%s'.", rootRedirect, config.RootRedirectKey),
		Path:        "/",
		Tags:        []string{"Common"},
		// The target depends on the configuration, so the redirect must not be
		// cached permanently by the client.
		DefaultStatus: http.StatusFound,
	}, func(_ context.Context, _ *struct{}) (*response.RedirectResponse, error) {
		return &response.RedirectResponse{Location: rootRedirect}, nil
	})

}

// publicHumaConfig is the OpenAPI configuration of the publicly exposed API.
func publicHumaConfig() huma.Config {
	humaConfig := huma.DefaultConfig(config.StringSanitized("app.name"), config.String("app.version"))
	humaConfig.Info = &huma.Info{
		Title:       config.StringSanitized("app.name"),
		Description: config.String("app.description"),
		Contact: &huma.Contact{
			Name:  config.String("app.contact.name"),
			Email: config.String("app.contact.mail"),
		},
		License: nil,
		Version: config.String("app.version"),
	}
	humaConfig.OpenAPIPath = openAPIPath
	humaConfig.DocsPath = docsPath
	humaConfig.SchemasPath = schemasPath

	// Merged into the SwaggerUIBundle configuration by Huma. Only the options
	// which Swagger UI reads from that object work here; everything which it
	// takes through initOAuth() does not, because Huma never calls it.
	humaConfig.DocsRendererConfig = map[string]any{
		// Keeps a pasted token in the browser, so it survives a reload.
		"persistAuthorization": true,
	}

	// No server is configured on purpose. Without it the clients call the host
	// they loaded the specification from, which keeps the API host independent.
	humaConfig.Servers = nil

	humaConfig.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		// Both schemes end up as the same 'Authorization: Bearer <token>'
		// header, so the API cannot tell them apart and does not have to.
		bearerScheme: {
			Type:         "http",
			Scheme:       "bearer",
			BearerFormat: "JWT",
			Description:  "Paste a token which was handed out by the issuer. This is the way to authenticate in this documentation.",
		},
		oauthScheme: {
			Type: "oauth2",
			// This documents how the API is secured, for a generated client.
			// The flow cannot be run from this documentation: Swagger UI needs
			// an 'oauth2-redirect.html' for it, which Huma does not serve. Use
			// the bearer scheme above with a token from another client.
			Description: "How a client obtains a token.",
			Flows: &huma.OAuthFlows{
				AuthorizationCode: &huma.OAuthFlow{
					AuthorizationURL: fmt.Sprintf("%s/oauth/v2/authorize", config.String("authentication.oidc.issuer")),
					TokenURL:         fmt.Sprintf("%s/oauth/v2/token", config.String("authentication.oidc.issuer")),
					RefreshURL:       fmt.Sprintf("%s/oauth/v2/token", config.String("authentication.oidc.issuer")),
					Scopes: map[string]string{
						"openid":         "To return the openid basic information.",
						"profile":        "To return the profile attributes like name.",
						"email":          "To return the email address.",
						"offline_access": "To access the user's data offline.",
					},
				},
			},
		},
	}

	humaConfig.DocsRenderer = huma.DocsRendererSwaggerUI

	return humaConfig
}

// registerAuthentication puts the verification of the tokens in front of every
// operation which declares the security scheme. While it is off, the endpoints
// answer without a token, which is why it is logged as loud as it is.
func registerAuthentication(ctx context.Context, api huma.API) error {
	if config.Bool(config.AuthenticationSkipKey) {
		// Only reachable while developing; every other environment refuses to
		// start with it. It still gets logged as loud as this, because an API
		// which verifies nothing must not be a quiet surprise.
		zap.L().Warn("THE PUBLIC API VERIFIES NO TOKEN",
			zap.String("reason", fmt.Sprintf("'%s' is on", config.AuthenticationSkipKey)),
			zap.String("environment", config.GetEnvironment().String()),
		)
		return nil
	}

	authenticator, err := newAuthenticator(ctx)
	if err != nil {
		return err
	}

	api.UseMiddleware(authenticator.middleware(api))

	zap.L().Info("The public API verifies the tokens of the issuer",
		zap.String("issuer", config.String(config.OidcIssuerKey)),
	)
	return nil
}

// authenticated declares that an operation only answers with a valid token. The
// two schemes are alternatives: whether the token was pasted or fetched by the
// flow, it arrives in the same header.
func authenticated() []map[string][]string {
	return []map[string][]string{
		{bearerScheme: {}},
		{oauthScheme: {"openid"}},
	}
}
