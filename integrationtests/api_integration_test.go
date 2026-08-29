//go:build integration

package integrationtests

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// '/' points at the documentation unless the configuration says otherwise.
func Test_API_Root_Redirects(t *testing.T) {
	response := doRequest(t, http.MethodGet, PublicURL, "/", nil, nil)
	_ = readBody(t, response)

	require.Equal(t, http.StatusFound, response.StatusCode)
	require.Equal(t, "/docs", response.Header.Get("Location"))
}

func Test_API_Docs(t *testing.T) {
	response := doRequest(t, http.MethodGet, PublicURL, "/docs", nil, nil)
	body := readBody(t, response)

	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Contains(t, response.Header.Get("Content-Type"), "text/html")
	require.Contains(t, body, "swagger-ui")
}

// The specification has to describe both ways of authenticating, so a generated
// client knows what the API expects.
func Test_API_OpenAPI(t *testing.T) {
	response := doRequest(t, http.MethodGet, PublicURL, "/openapi.json", nil, nil)
	body := readBody(t, response)

	require.Equal(t, http.StatusOK, response.StatusCode)

	var document struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
		Components struct {
			SecuritySchemes map[string]struct {
				Type   string `json:"type"`
				Scheme string `json:"scheme"`
			} `json:"securitySchemes"`
		} `json:"components"`
		Paths map[string]any `json:"paths"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &document))

	require.NotEmpty(t, document.Info.Version)
	require.Contains(t, document.Paths, "/ping")

	bearer, ok := document.Components.SecuritySchemes["bearer"]
	require.True(t, ok, "the bearer scheme is what lets a token be pasted into the documentation")
	require.Equal(t, "http", bearer.Type)
	require.Equal(t, "bearer", bearer.Scheme)

	_, ok = document.Components.SecuritySchemes["oauth2"]
	require.True(t, ok, "the oauth2 scheme documents how a client obtains a token")
}

// The whole suite runs with the verification skipped, so the protected endpoint
// answers. That it refuses without a token is covered by the unit tests of the
// middleware, which do not need a database.
func Test_API_Ping(t *testing.T) {
	response := doRequest(t, http.MethodGet, PublicURL, "/ping", nil, nil)
	body := readBody(t, response)

	require.Equal(t, http.StatusOK, response.StatusCode)

	var ping struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &ping))
	require.Equal(t, "pong", ping.Message)
}

// The management API documents itself as well, on its own port.
func Test_API_ManagementHasItsOwnSpecification(t *testing.T) {
	response := doRequest(t, http.MethodGet, ManagementURL, "/openapi.json", nil, nil)
	body := readBody(t, response)

	require.Equal(t, http.StatusOK, response.StatusCode)

	var document struct {
		Paths map[string]any `json:"paths"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &document))
	require.Contains(t, document.Paths, "/health/readiness")
	require.NotContains(t, document.Paths, "/ping", "the business endpoints do not belong on the management API")
}
