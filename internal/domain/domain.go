// Package domain holds the business logic of the application. It sits between
// the API and the repository: the API only turns a request into a call and the
// answer into a response, the repository only reads and writes, and everything
// which decides something happens here.
package domain

import "otel-playground/internal/infrastructure/repository"

// Service bundles the service of every entity. Every field is an interface, so
// a test of the API can replace a single one with a fake.
type Service struct {
	HealthService HealthService
}

// NewService builds every service on top of the same repository. Each of them
// also gets the service itself, so one service can use another one instead of
// reaching into the repository of a different entity.
func NewService(repository *repository.Repository) *Service {
	service := Service{}
	service.HealthService = NewHealthService(repository, &service)

	return &service
}
