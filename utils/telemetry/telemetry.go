package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"otel-playground/internal/config"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// instrumentationName identifies this application as the source of the emitted
// log records.
const instrumentationName = "github.com/m-mattia-m/OTEL-Playground"

// Setup registers the global trace and logger providers and routes zap through
// the OTel bridge, so zap.L() writes JSON to stdout and OTLP log records to the
// collector. The returned function flushes both providers and has to be called
// on shutdown.
func Setup(ctx context.Context) (func(context.Context) error, error) {
	otelResource := newResource()
	endpoint := config.String("otel.exporter.otlp.endpoint")
	// The collector is reachable over plain HTTP during local development.
	insecure := config.GetEnvironment() == config.Development

	traceProvider, err := newTraceProvider(ctx, otelResource, endpoint, insecure)
	if err != nil {
		return nil, err
	}
	otel.SetTracerProvider(traceProvider)

	loggerProvider, err := newLoggerProvider(ctx, otelResource, endpoint, insecure)
	if err != nil {
		return nil, errors.Join(err, traceProvider.Shutdown(ctx))
	}
	global.SetLoggerProvider(loggerProvider)

	zap.ReplaceGlobals(newLogger(loggerProvider))

	return func(ctx context.Context) error {
		return errors.Join(
			traceProvider.Shutdown(ctx),
			loggerProvider.Shutdown(ctx),
		)
	}, nil
}

// Tracer is the tracer of this application. Every span which is started with it
// ends up under the same instrumentation scope as the log records, so a trace
// and its logs can be correlated in the backend.
func Tracer() trace.Tracer {
	return otel.Tracer(instrumentationName)
}

// newResource describes the service every span and log record is attributed to.
func newResource() *resource.Resource {
	return resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(config.StringSanitized("app.name")),
		semconv.ServiceVersion(config.String("app.version")),
		semconv.DeploymentEnvironmentName(config.GetEnvironment().String()),
	)
}

func newTraceProvider(ctx context.Context, otelResource *resource.Resource, endpoint string, insecure bool) (*sdktrace.TracerProvider, error) {
	options := []otlptracehttp.Option{otlptracehttp.WithEndpoint(endpoint)}
	if insecure {
		options = append(options, otlptracehttp.WithInsecure())
	}

	exporter, err := otlptracehttp.New(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize the trace exporter: %w", err)
	}

	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(otelResource),
	), nil
}

func newLoggerProvider(ctx context.Context, otelResource *resource.Resource, endpoint string, insecure bool) (*sdklog.LoggerProvider, error) {
	options := []otlploghttp.Option{otlploghttp.WithEndpoint(endpoint)}
	if insecure {
		options = append(options, otlploghttp.WithInsecure())
	}

	exporter, err := otlploghttp.New(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize the log exporter: %w", err)
	}

	return sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
		sdklog.WithResource(otelResource),
	), nil
}

// newLogger tees every record to stdout as JSON and to the OTel bridge, which
// forwards it over OTLP to the collector.
func newLogger(loggerProvider *sdklog.LoggerProvider) *zap.Logger {
	level := config.GetLogLevel()

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	encoderConfig.EncodeDuration = zapcore.SecondsDurationEncoder

	consoleCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.AddSync(os.Stdout),
		level,
	)

	otelCore := otelzap.NewCore(
		instrumentationName,
		otelzap.WithLoggerProvider(loggerProvider),
	)

	return zap.New(
		zapcore.NewTee(consoleCore, otelCore),
		zap.AddCaller(),
		// The bridge has no level of its own, so both sinks only honour the
		// configured level if it is applied to the logger as well.
		zap.IncreaseLevel(level),
	)
}
