package zaphelper

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

func TraceID(ctx context.Context) zap.Field {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return zap.Skip()
	}
	return zap.String("trace_id", sc.TraceID().String())
}
