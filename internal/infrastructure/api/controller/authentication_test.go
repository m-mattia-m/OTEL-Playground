package controller

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"otel-playground/internal/config"

	"github.com/stretchr/testify/require"
)

// issuer is an OIDC issuer which serves a real discovery document and a real
// JWKS, and signs real RS256 tokens. That way the middleware is tested against
// the same code paths a Keycloak or a Zitadel would drive.
type issuer struct {
	url string
	key *rsa.PrivateKey
}

func rawURL(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func newIssuer(t *testing.T) *issuer {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                server.URL,
			"authorization_endpoint":                server.URL + "/authorize",
			"token_endpoint":                        server.URL + "/token",
			"jwks_uri":                              server.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		exponent := make([]byte, 4)
		binary.BigEndian.PutUint32(exponent, uint32(key.PublicKey.E))
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "test",
			"n": rawURL(key.PublicKey.N.Bytes()), "e": rawURL(exponent[1:]),
		}}})
	})

	return &issuer{url: server.URL, key: key}
}

// token signs a token for the given audience and expiry.
func (i *issuer) token(t *testing.T, subject, audience string, expiry time.Time) string {
	t.Helper()

	header := rawURL([]byte(`{"alg":"RS256","typ":"JWT","kid":"test"}`))
	payload, err := json.Marshal(map[string]any{
		"iss": i.url, "sub": subject, "aud": audience,
		"exp": expiry.Unix(), "iat": time.Now().Unix(),
	})
	require.NoError(t, err)

	signed := header + "." + rawURL(payload)
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, i.key, crypto.SHA256, digest[:])
	require.NoError(t, err)

	return signed + "." + rawURL(signature)
}

// otherIssuer is a second issuer, used to prove that a token signed by somebody
// else is not accepted.
func (i *issuer) configure(t *testing.T, audience string) {
	t.Helper()

	t.Setenv("AUTHENTICATION_SKIP", "false")
	t.Setenv("APP_ENV", "development")
	t.Setenv("AUTHENTICATION_OIDC_ISSUER", i.url)
	t.Setenv("AUTHENTICATION_OIDC_CLIENTID", "test-client")
	t.Setenv("AUTHENTICATION_OIDC_AUDIENCE", audience)
	require.NoError(t, config.Load())
}

func TestProtectedEndpointNeedsAValidToken(t *testing.T) {
	idp := newIssuer(t)
	idp.configure(t, "test-client")

	engine, err := newPublicRouter(context.Background())
	require.NoError(t, err)

	valid := idp.token(t, "user-1", "test-client", time.Now().Add(time.Hour))

	for _, tc := range []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"not a bearer token", "Basic dXNlcjpwYXNz", http.StatusUnauthorized},
		{"empty bearer token", "Bearer ", http.StatusUnauthorized},
		{"not a jwt", "Bearer not-a-jwt", http.StatusUnauthorized},
		{"wrong audience", "Bearer " + idp.token(t, "user-1", "somebody-else", time.Now().Add(time.Hour)), http.StatusUnauthorized},
		{"expired", "Bearer " + idp.token(t, "user-1", "test-client", time.Now().Add(-time.Hour)), http.StatusUnauthorized},
		{"valid", "Bearer " + valid, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := map[string]string{}
			if tc.header != "" {
				header["Authorization"] = tc.header
			}

			response := get(t, engine, "/ping", header)
			require.Equal(t, tc.want, response.Code)
		})
	}
}

// Why a token was rejected helps somebody who guesses more than it helps the
// caller, so every rejection has to look the same and carry no detail.
func TestRejectionLeaksNothing(t *testing.T) {
	idp := newIssuer(t)
	idp.configure(t, "test-client")

	engine, err := newPublicRouter(context.Background())
	require.NoError(t, err)

	expired := idp.token(t, "user-1", "test-client", time.Now().Add(-time.Hour))
	response := get(t, engine, "/ping", map[string]string{"Authorization": "Bearer " + expired})

	require.Equal(t, http.StatusUnauthorized, response.Code)
	body := response.Body.String()
	require.NotContains(t, body, "expired")
	require.NotContains(t, body, idp.url, "the issuer must not be named in the answer")
	require.NotContains(t, body, "audience")
}

// A token which was signed by a different issuer must not be accepted, even if
// every claim inside it looks right.
func TestTokenOfAnotherIssuerIsRejected(t *testing.T) {
	idp := newIssuer(t)
	foreign := newIssuer(t)
	idp.configure(t, "test-client")

	engine, err := newPublicRouter(context.Background())
	require.NoError(t, err)

	stolen := foreign.token(t, "user-1", "test-client", time.Now().Add(time.Hour))
	response := get(t, engine, "/ping", map[string]string{"Authorization": "Bearer " + stolen})

	require.Equal(t, http.StatusUnauthorized, response.Code)
}

// The audience is what keeps a token which was minted for another service from
// being replayed against this one.
func TestAudienceIsChecked(t *testing.T) {
	idp := newIssuer(t)
	idp.configure(t, "otel-playground-api")

	engine, err := newPublicRouter(context.Background())
	require.NoError(t, err)

	require.Equal(t, http.StatusOK,
		get(t, engine, "/ping", map[string]string{
			"Authorization": "Bearer " + idp.token(t, "user-1", "otel-playground-api", time.Now().Add(time.Hour)),
		}).Code)

	require.Equal(t, http.StatusUnauthorized,
		get(t, engine, "/ping", map[string]string{
			"Authorization": "Bearer " + idp.token(t, "user-1", "test-client", time.Now().Add(time.Hour)),
		}).Code, "a token for another audience must not be accepted")
}

// While the verification is on, an endpoint without a security scheme still
// answers without a token.
func TestUnprotectedEndpointsStayOpenWhileVerifying(t *testing.T) {
	idp := newIssuer(t)
	idp.configure(t, "test-client")

	engine, err := newPublicRouter(context.Background())
	require.NoError(t, err)

	require.Equal(t, http.StatusFound, get(t, engine, "/", nil).Code)
	require.Equal(t, http.StatusOK, get(t, engine, "/docs", nil).Code)
	require.Equal(t, http.StatusOK, get(t, engine, "/openapi.json", nil).Code)
}
