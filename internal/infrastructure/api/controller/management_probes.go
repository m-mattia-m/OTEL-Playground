package controller

import (
	"context"
	"net/http"
	"otel-playground/internal/domain"
	"otel-playground/internal/infrastructure/api/response"
	"reflect"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// readinessTimeout bounds the checks of the readiness probe. It has to stay
// below the timeout of the caller, so the probe answers with a 503 instead of
// letting the platform run into its own timeout.
const readinessTimeout = 2 * time.Second

// livenessHandler answers whether the process is running. It must not check a
// single dependency: a failing liveness probe restarts the process, and
// restarting it does not bring a database back.
func livenessHandler(_ context.Context, _ *struct{}) (*response.LivenessResponse, error) {
	return &response.LivenessResponse{Body: response.Liveness{Status: response.LivenessAlive}}, nil
}

// readinessHandler answers whether the application can serve traffic. Which
// dependency has to be usable for that is decided by the domain; this handler
// only turns the answer into a response.
func readinessHandler(service *domain.Service) func(context.Context, *struct{}) (*response.ReadinessResponse, error) {
	return func(ctx context.Context, _ *struct{}) (*response.ReadinessResponse, error) {
		ctx, cancel := context.WithTimeout(ctx, readinessTimeout)
		defer cancel()

		readiness := response.NewReadiness(
			readinessState(service.HealthService.Readiness(ctx).Database),
		)

		return &response.ReadinessResponse{
			Status: readinessStatusCode(readiness),
			Body:   readiness,
		}, nil
	}
}

// readinessState turns the state of a single dependency into the value the API
// answers with.
func readinessState(usable bool) response.ReadinessState {
	if !usable {
		return response.ReadinessNotReady
	}
	return response.ReadinessReady
}

// readinessStatusCode is what the platform actually acts on. The body is only
// there for a human, so an unusable dependency has to show up as a 503 as well,
// otherwise the traffic keeps being routed to this instance.
func readinessStatusCode(readiness response.Readiness) int {
	if readiness.Status != response.ReadinessReady {
		return http.StatusServiceUnavailable
	}
	return http.StatusOK
}

// readinessResponses declares the 503 of the readiness probe in the OpenAPI
// specification. Huma only derives the status the output struct is registered
// with, which is the 200, so without this the specification would claim that a
// failing probe answers with the generic error model instead of the body which
// names the dependency.
func readinessResponses(api huma.API) map[string]*huma.Response {
	schema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[response.Readiness](), true, "Readiness")

	return map[string]*huma.Response{
		strconv.Itoa(http.StatusServiceUnavailable): {
			Description: "At least one dependency is not usable, so no traffic should be routed to this instance.",
			Content: map[string]*huma.MediaType{
				"application/json": {Schema: schema},
			},
		},
	}
}
