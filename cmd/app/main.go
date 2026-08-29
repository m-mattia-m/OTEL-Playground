package main

import (
	"context"
	"log"
	"otel-playground/internal/config"
	"otel-playground/internal/domain"
	"otel-playground/internal/infrastructure/api/controller"
	"otel-playground/internal/infrastructure/repository"
	"otel-playground/utils/telemetry"
	"time"

	"go.uber.org/zap"
)

// shutdownTimeout bounds the final flush of the traces and the log records.
const shutdownTimeout = 5 * time.Second

func main() {
	if err := config.Load(); err != nil {
		log.Fatal("Error loading configuration. Error: ", err)
	}

	flush, err := telemetry.Setup(context.Background())
	if err != nil {
		log.Fatalf("Failed to initialize the telemetry: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := flush(ctx); err != nil {
			log.Printf("telemetry shutdown: %v", err)
		}
	}()

	repo, err := repository.NewRepository(context.Background())
	if err != nil {
		zap.L().Panic("Failed to initialize the repository", zap.Error(err))
	}
	defer repo.Close()

	routers, err := controller.NewRouters(context.Background(), domain.NewService(repo))
	if err != nil {
		zap.L().Panic("Failed to initialize the routers", zap.Error(err))
	}

	zap.L().Debug("Following configuration is used: ", zap.Any("config", config.Get()))

	// The management API runs behind its own port, so the probes and the metrics
	// are not reachable over the publicly exposed port.
	go func() {
		zap.L().Info("Starting the management API", zap.String("address", routers.ManagementAddress))
		if err := routers.Management.Run(routers.ManagementAddress); err != nil {
			zap.L().Panic("Failed to start the management router", zap.Error(err))
		}
	}()

	zap.L().Info("Starting the public API", zap.String("address", routers.PublicAddress))
	if err := routers.Public.Run(routers.PublicAddress); err != nil {
		zap.L().Panic("Failed to start the public router", zap.Error(err))
	}
}
