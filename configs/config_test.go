package configs

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyEnvOverridesSetsHTTPCIDRControls(t *testing.T) {
	t.Setenv("HTTP_ALLOWED_CIDRS", "203.0.113.10/32,198.51.100.0/24")
	t.Setenv("HTTP_TRUSTED_PROXY_CIDRS", "10.0.0.0/8")

	cfg := &Config{}
	require.NoError(t, applyEnvOverrides(cfg))
	require.Equal(t, []string{"203.0.113.10/32", "198.51.100.0/24"}, cfg.HTTP.AllowedCIDRs)
	require.Equal(t, []string{"10.0.0.0/8"}, cfg.HTTP.TrustedProxyCIDRs)
}

func TestApplyEnvOverridesRejectsInvalidRateLimit(t *testing.T) {
	t.Setenv("HTTP_RATE_LIMIT_PER_MINUTE", "fast")
	t.Setenv("HTTP_ALLOWED_CIDRS", "")
	t.Setenv("HTTP_TRUSTED_PROXY_CIDRS", "")

	cfg := &Config{}
	err := applyEnvOverrides(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid HTTP_RATE_LIMIT_PER_MINUTE")
}

func TestLookupEnvTrimsWhitespace(t *testing.T) {
	require.NoError(t, os.Setenv("AGENTOPT_LOOKUP_ENV_TEST", "  value  "))
	t.Cleanup(func() {
		_ = os.Unsetenv("AGENTOPT_LOOKUP_ENV_TEST")
	})

	value, ok := lookupEnv("AGENTOPT_LOOKUP_ENV_TEST")
	require.True(t, ok)
	require.Equal(t, "value", value)
}
