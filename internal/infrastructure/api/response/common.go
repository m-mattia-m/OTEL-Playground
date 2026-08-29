// Package response contains the response structures which are returned by the
// API endpoints. They are the Huma output structs, which means they also
// describe how the response is documented in the OpenAPI specification.
package response

// Ping is the body of the ping response.
type Ping struct {
	Message string `json:"message" example:"pong" doc:"Static answer which proves that the API is reachable."`
}

// PingResponse is returned by the ping endpoint.
type PingResponse struct {
	Body Ping
}

// RedirectResponse is returned by endpoints which only point the client to
// another location. It has no body, the whole information is in the header.
type RedirectResponse struct {
	Location string `header:"Location" doc:"Location the client gets redirected to."`
}

// DocumentResponse is returned by endpoints which serve a whole document
// instead of a JSON object, like the self-hosted Swagger UI. The body is
// written as-is, without any marshalling.
type DocumentResponse struct {
	ContentType string `header:"Content-Type" doc:"Media type of the returned document."`
	Body        []byte
}
