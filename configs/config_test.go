package configs

import (
	"os"
	"path/filepath"
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

func TestApplyEnvOverridesLoadsBootstrapUsersFromFile(t *testing.T) {
	usersFile := filepath.Join(t.TempDir(), "bootstrap-users.json")
	require.NoError(t, os.WriteFile(usersFile, []byte(`[
		{
			"id": "beta-user-1",
			"org_id": "beta-org",
			"org_name": "Beta Org",
			"email": "beta@example.com",
			"name": "Beta Operator",
			"password": "secret"
		}
	]`), 0o644))

	t.Setenv("AUTH_BOOTSTRAP_USERS_JSON", `[{"id":"beta-user-json","org_id":"json-org","org_name":"JSON Org","email":"json@example.com","name":"JSON User","password":"json-secret"}]`)
	t.Setenv("AUTH_BOOTSTRAP_USERS_FILE", usersFile)

	cfg := &Config{}
	require.NoError(t, applyEnvOverrides(cfg))
	require.Len(t, cfg.Auth.BootstrapUsers, 1)
	require.Equal(t, "beta-user-1", cfg.Auth.BootstrapUsers[0].ID)
	require.Equal(t, "beta@example.com", cfg.Auth.BootstrapUsers[0].Email)
}

func TestApplyEnvOverridesRejectsInvalidBootstrapUsersFile(t *testing.T) {
	usersFile := filepath.Join(t.TempDir(), "bootstrap-users.json")
	require.NoError(t, os.WriteFile(usersFile, []byte(`{bad json`), 0o644))
	t.Setenv("AUTH_BOOTSTRAP_USERS_FILE", usersFile)

	cfg := &Config{}
	err := applyEnvOverrides(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid AUTH_BOOTSTRAP_USERS_FILE")
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

func TestConfigValidateRejectsInvalidReleaseSecurityConfig(t *testing.T) {
	cfg := &Config{
		App: App{
			Mode: "prod",
		},
		DB: DB{
			Dialect: "sqlite3",
			DSN:     "data/agentopt.db?_fk=1",
		},
		Auth: Auth{
			AllowDemoUser:      true,
			StaticTokenEnabled: true,
		},
	}

	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "Jwt.Secret is required in release mode")
	require.Contains(t, err.Error(), "Auth.AllowDemoUser must be false in release mode")
	require.Contains(t, err.Error(), "Auth.StaticTokenEnabled must be false in release mode")
	require.Contains(t, err.Error(), "App.APIToken is required when Auth.StaticTokenEnabled is true")
}

func TestConfigValidateRejectsInvalidCIDRsAndBootstrapUsers(t *testing.T) {
	cfg := &Config{
		App: App{
			Mode: "local",
		},
		DB: DB{
			Dialect: "sqlite3",
			DSN:     "data/agentopt-local.db?_fk=1",
		},
		HTTP: HTTP{
			AllowedCIDRs:      []string{"not-a-cidr"},
			TrustedProxyCIDRs: []string{"10.0.0.0/8", "also-bad"},
		},
		Auth: Auth{
			BootstrapUsers: []BootstrapUser{
				{
					ID:      "beta-user-1",
					OrgID:   "beta-org",
					OrgName: "Beta Org",
					Email:   "beta@example.com",
					Name:    "",
				},
				{
					ID:       "beta-user-1",
					OrgID:    "beta-org",
					OrgName:  "Beta Org",
					Email:    "beta@example.com",
					Name:     "Beta Operator",
					Password: "secret",
				},
			},
		},
	}

	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), `HTTP.AllowedCIDRs contains invalid CIDR "not-a-cidr"`)
	require.Contains(t, err.Error(), `HTTP.TrustedProxyCIDRs contains invalid CIDR "also-bad"`)
	require.Contains(t, err.Error(), "Auth.BootstrapUsers[0].Name is required")
	require.Contains(t, err.Error(), "Auth.BootstrapUsers[0].Password is required")
	require.Contains(t, err.Error(), "Auth.BootstrapUsers[1].ID must be unique")
	require.Contains(t, err.Error(), "Auth.BootstrapUsers[1].Email must be unique")
}

func TestConfigValidateAllowsLocalClosedBetaDefaults(t *testing.T) {
	cfg := &Config{
		App: App{
			Mode:     "local",
			APIToken: "agentopt-dev-token",
		},
		DB: DB{
			Dialect: "sqlite3",
			DSN:     "data/agentopt-local.db?_fk=1",
		},
		Jwt: Jwt{
			Secret: "dev-secret",
		},
		Auth: Auth{
			AllowDemoUser:      true,
			StaticTokenEnabled: true,
			BootstrapUsers: []BootstrapUser{
				{
					ID:       "beta-user-1",
					OrgID:    "beta-org",
					OrgName:  "Beta Org",
					Email:    "beta@example.com",
					Name:     "Beta Operator",
					Password: "secret",
				},
			},
		},
		HTTP: HTTP{
			AllowedCIDRs:      []string{"127.0.0.1/32"},
			TrustedProxyCIDRs: []string{"10.0.0.0/8"},
		},
	}

	require.NoError(t, cfg.Validate())
}
