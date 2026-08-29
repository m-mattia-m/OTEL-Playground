package response

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The overall status is derived, so a dependency which is not usable can never
// be reported as an application which is.
func TestNewReadinessDerivesTheStatus(t *testing.T) {
	ready := NewReadiness(ReadinessReady)
	require.Equal(t, ReadinessReady, ready.Status)
	require.Equal(t, ReadinessReady, ready.Database)

	broken := NewReadiness(ReadinessNotReady)
	require.Equal(t, ReadinessNotReady, broken.Status, "a broken dependency has to sink the overall status")
	require.Equal(t, ReadinessNotReady, broken.Database)
}
