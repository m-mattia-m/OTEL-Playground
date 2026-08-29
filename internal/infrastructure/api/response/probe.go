package response

// LivenessState is the state which is reported by the liveness probe.
type LivenessState string

// ReadinessState is the state which is reported by the readiness probe.
type ReadinessState string

const (
	// LivenessAlive means that the process is running and able to answer.
	LivenessAlive LivenessState = "alive"
	// ReadinessReady means that the application is able to serve traffic.
	ReadinessReady ReadinessState = "ready"
	// ReadinessNotReady means that a dependency is not usable. It is reported
	// for the single dependency as well as for the application as a whole.
	ReadinessNotReady ReadinessState = "not_ready"
)

// Liveness is the body of the liveness probe response. Probes are only exposed
// on the management port, but they still only report a state and no internal
// details like versions, dependencies or error messages.
type Liveness struct {
	Status LivenessState `json:"status" enum:"alive" example:"alive" doc:"State of the process."`
}

// Readiness is the body of the readiness probe response. It names every
// dependency on its own, so it is visible which one keeps the instance out of
// the load balancer. It never carries the reason, because that one would
// describe the inside of the deployment.
type Readiness struct {
	Status   ReadinessState `json:"status" enum:"ready,not_ready" example:"ready" doc:"State of the application. It is 'not_ready' as soon as one of the dependencies below is."`
	Database ReadinessState `json:"database" enum:"ready,not_ready" example:"ready" doc:"State of the database connection."`
}

// NewReadiness builds the body out of the state of every dependency and derives
// the overall status from them. Deriving it here means that a dependency which
// gets added later cannot be forgotten in the overall status, because the
// compiler asks every caller for it.
func NewReadiness(database ReadinessState) Readiness {
	readiness := Readiness{
		Status:   ReadinessReady,
		Database: database,
	}

	if database != ReadinessReady {
		readiness.Status = ReadinessNotReady
	}

	return readiness
}

// LivenessResponse is returned by the liveness probe.
type LivenessResponse struct {
	Body Liveness
}

// ReadinessResponse is returned by the readiness probe. Status carries the HTTP
// status code, so the probe can answer with a 503 and still return the body
// which names the broken dependency.
type ReadinessResponse struct {
	Status int
	Body   Readiness
}
