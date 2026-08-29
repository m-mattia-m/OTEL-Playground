package controller

import (
	"context"
	"fmt"
	"net/http"
	"otel-playground/internal/config"
	"otel-playground/utils/telemetry"
	"otel-playground/utils/zaphelper"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/danielgtaylor/huma/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.uber.org/zap"
)

// bearerPrefix is the scheme of the Authorization header the tokens arrive in.
const bearerPrefix = "Bearer "

// contextKey is the type of the values this middleware puts into the context.
// A own type keeps them apart from the values of every other package.
type contextKey string

// subjectContextKey holds the 'sub' claim of the verified token, which is the
// stable identifier of the caller.
const subjectContextKey contextKey = "subject"

// Subject returns the caller of the request, or an empty string if the endpoint
// did not require a token.
func Subject(ctx context.Context) string {
	subject, _ := ctx.Value(subjectContextKey).(string)
	return subject
}

// authenticator verifies the tokens which the configured issuer handed out.
type authenticator struct {
	verifier *oidc.IDTokenVerifier
}

// newAuthenticator asks the issuer for its configuration, so a wrong issuer
// fails the startup instead of every single request. The signing keys are
// fetched on demand and cached afterwards, which means a rotated key does not
// need a restart.
func newAuthenticator(ctx context.Context) (*authenticator, error) {
	issuer := config.String(config.OidcIssuerKey)

	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("failed to read the configuration of the issuer %q: %w", issuer, err)
	}

	return &authenticator{
		verifier: provider.Verifier(&oidc.Config{
			// go-oidc calls it ClientID, but what it checks is the 'aud' claim.
			ClientID: config.OidcAudience(),
		}),
	}, nil
}

// middleware rejects every request which does not carry a valid token. It only
// looks at the operations which declare a security scheme, so the entrypoint
// and the documentation stay reachable without one.
func (a *authenticator) middleware(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		operation := ctx.Operation()
		if operation == nil || len(operation.Security) == 0 {
			next(ctx)
			return
		}

		requestCtx, span := telemetry.Tracer().Start(ctx.Context(), "authenticate")
		defer span.End()

		subject, err := a.verify(requestCtx, ctx.Header("Authorization"))
		if err != nil {
			// Why a token was rejected tells somebody who guesses more than it
			// tells the caller, so the answer stays the same for every reason
			// and only the log carries the detail.
			zap.L().Info("Rejected a request without a valid token",
				zap.String("operation", operation.OperationID),
				zap.Error(err),
				zaphelper.TraceID(requestCtx),
			)
			span.SetStatus(codes.Error, "unauthenticated")

			huma.WriteErr(api, ctx, http.StatusUnauthorized, "unauthorized")
			return
		}

		span.SetAttributes(attribute.String("enduser.id", subject))
		next(huma.WithValue(huma.WithContext(ctx, requestCtx), subjectContextKey, subject))
	}
}

// verify reads the token out of the Authorization header and checks it against
// the issuer. It returns the caller the token was handed out for.
func (a *authenticator) verify(ctx context.Context, header string) (string, error) {
	if !strings.HasPrefix(header, bearerPrefix) {
		return "", fmt.Errorf("the Authorization header is missing or is not a bearer token")
	}

	rawToken := strings.TrimSpace(strings.TrimPrefix(header, bearerPrefix))
	if rawToken == "" {
		return "", fmt.Errorf("the bearer token is empty")
	}

	// Verify checks the signature against the keys of the issuer as well as the
	// issuer, the audience and the expiry of the token.
	token, err := a.verifier.Verify(ctx, rawToken)
	if err != nil {
		return "", fmt.Errorf("the token was not accepted: %w", err)
	}

	return token.Subject, nil
}
