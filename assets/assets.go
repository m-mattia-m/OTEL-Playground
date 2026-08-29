// Package assets contains the static files which are served by the API. They
// are embedded into the binary so that serving them does not depend on the
// working directory the application was started from.
package assets

import _ "embed"

// SwaggerUI is the self-hosted Swagger UI document which renders the OpenAPI
// specification exposed under the OpenAPI path of the API.
//
//go:embed swagger-ui.html
var SwaggerUI []byte
