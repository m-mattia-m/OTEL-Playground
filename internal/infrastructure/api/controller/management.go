package controller

import (
	"net/http"
	"otel-playground/internal/config"
	"otel-playground/internal/domain"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
)

// newManagementRouter builds the internal API. It runs behind its own port so
// that only the platform (Kubernetes, the scraper, ...) can reach the probes
// and the metrics, while the public port only exposes the business endpoints.
func newManagementRouter(service *domain.Service) (*gin.Engine, error) {
	engine, err := newEngine()
	if err != nil {
		return nil, err
	}

	api := humagin.New(engine, managementHumaConfig())
	registerRootRedirect(api)

	huma.Register(api, huma.Operation{
		Method:      http.MethodGet,
		OperationID: "get-liveness",
		Summary:     "Liveness probe",
		Description: "Reports whether the process is running. A failing liveness probe means the process has to be restarted.",
		Path:        "/health/liveness",
		Tags:        []string{"Probes"},
	}, livenessHandler)

	huma.Register(api, huma.Operation{
		Method:      http.MethodGet,
		OperationID: "get-readiness",
		Summary:     "Readiness probe",
		Description: "Reports whether the application is able to serve traffic, and which of its dependencies is not usable. A failing readiness probe answers with a 503 and means no traffic gets routed to this instance.",
		Path:        "/health/readiness",
		Tags:        []string{"Probes"},
		Responses:   readinessResponses(api),
	}, readinessHandler(service))

	if config.Bool("management.metrics.enabled") {
		// TODO: register the metrics endpoint here, on the management API, so it
		//  shares the port with the probes and stays unexposed.
		//svc.MetricsService.SetupExtendedPrometheusMetrics()
		//engine.GET(config.StringOr("management.metrics.path", "/metrics"), gin.WrapH(promhttp.Handler()))
	}

	return engine, nil
}

// managementHumaConfig is the OpenAPI configuration of the internal API. It
// documents itself the same way as the public API, since it is only reachable
// on the management port anyway.
func managementHumaConfig() huma.Config {
	humaConfig := huma.DefaultConfig(config.StringSanitized("app.name")+"-management", config.String("app.version"))
	humaConfig.Info = &huma.Info{
		Title:       config.StringSanitized("app.name") + "-management",
		Description: "Internal API of " + config.String("app.name") + " which holds the probes and the metrics.",
		Version:     config.String("app.version"),
	}
	humaConfig.OpenAPIPath = openAPIPath
	humaConfig.DocsPath = docsPath
	humaConfig.SchemasPath = schemasPath
	humaConfig.Servers = nil
	humaConfig.DocsRenderer = huma.DocsRendererSwaggerUI

	return humaConfig
}
