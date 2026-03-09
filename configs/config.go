package configs

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"

	"github.com/liushuangls/go-server-template/pkg/xslog"
)

type Config struct {
	App   App          `koanf:"App"`
	DB    DB           `koanf:"DB"`
	Redis Redis        `koanf:"Redis"`
	Log   xslog.Config `koanf:"Log"`
	Jwt   Jwt          `koanf:"Jwt"`
	Auth  Auth         `koanf:"Auth"`
	HTTP  HTTP         `koanf:"HTTP"`
}

type App struct {
	Addr      string `koanf:"Addr"`
	Mode      string `koanf:"Mode"`
	StorePath string `koanf:"StorePath"`
	APIToken  string `koanf:"APIToken"`
}

type DB struct {
	Dialect     string `koanf:"Dialect"`
	DSN         string `koanf:"DSN"`
	MaxIdle     int    `koanf:"MaxIdle"`
	MaxActive   int    `koanf:"MaxActive"`
	MaxLifetime int    `koanf:"MaxLifetime"`
	AutoMigrate bool   `koanf:"AutoMigrate"`
}

type Redis struct {
	Addr     string `koanf:"Addr"`
	DB       int    `koanf:"DB"`
	Password string `koanf:"Password"`
}

type Jwt struct {
	Secret string `koanf:"Secret"`
	Issuer string `koanf:"Issuer"`
}

type Auth struct {
	AllowDemoUser      bool            `koanf:"AllowDemoUser"`
	StaticTokenEnabled bool            `koanf:"StaticTokenEnabled"`
	BootstrapUsers     []BootstrapUser `koanf:"BootstrapUsers"`
}

type BootstrapUser struct {
	ID       string `json:"id" koanf:"ID"`
	OrgID    string `json:"org_id" koanf:"OrgID"`
	OrgName  string `json:"org_name" koanf:"OrgName"`
	Email    string `json:"email" koanf:"Email"`
	Name     string `json:"name" koanf:"Name"`
	Password string `json:"password" koanf:"Password"`
}

type HTTP struct {
	AllowedOrigins     []string `koanf:"AllowedOrigins"`
	AllowedCIDRs       []string `koanf:"AllowedCIDRs"`
	TrustedProxyCIDRs  []string `koanf:"TrustedProxyCIDRs"`
	RateLimitPerMinute int      `koanf:"RateLimitPerMinute"`
	LogToStdout        bool     `koanf:"LogToStdout"`
}

func (c *Config) IsDebugMode() bool {
	return c.App.Mode == "debug" || c.App.Mode == "local"
}

func (c *Config) IsReleaseMode() bool {
	return c.App.Mode == "release" || c.App.Mode == "prod"
}

func (c *Config) AllowsDemoUser() bool {
	if c.Auth.AllowDemoUser {
		return true
	}
	return !c.IsReleaseMode() && len(c.Auth.BootstrapUsers) == 0
}

func (c *Config) AllowsStaticToken() bool {
	if strings.TrimSpace(c.App.APIToken) == "" {
		return false
	}
	if c.Auth.StaticTokenEnabled {
		return true
	}
	return !c.IsReleaseMode()
}

func InitConfig() (*Config, error) {
	var (
		cfg Config
		err error
		k   = koanf.New(".")
	)
	mode := os.Getenv("APP_MODE")
	if mode == "" {
		mode = "prod"
	}
	configPath := fmt.Sprintf("configs/%s.yaml", mode)

	err = k.Load(file.Provider(configPath), yaml.Parser())
	if err != nil {
		return nil, fmt.Errorf("error loading file config: %v", err)
	}

	err = k.Load(env.Provider("_", env.Opt{
		Prefix: "",
		TransformFunc: func(k, v string) (string, any) {
			return k, v
		},
	}), nil)
	if err != nil {
		return nil, fmt.Errorf("error loading env config: %v", err)
	}

	err = k.Unmarshal("", &cfg)
	if err != nil {
		return nil, fmt.Errorf("error unmarshal config: %v", err)
	}

	if err := applyEnvOverrides(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func applyEnvOverrides(cfg *Config) error {
	if value, ok := lookupEnv("APP_MODE"); ok {
		cfg.App.Mode = value
	}
	if value, ok := lookupEnv("APP_ADDR"); ok {
		cfg.App.Addr = value
	}
	if value, ok := lookupEnv("APP_STORE_PATH"); ok {
		cfg.App.StorePath = value
	}
	if value, ok := lookupEnv("APP_API_TOKEN"); ok {
		cfg.App.APIToken = value
	}
	if value, ok := lookupEnv("DB_DIALECT"); ok {
		cfg.DB.Dialect = value
	}
	if value, ok := lookupEnv("DB_DSN"); ok {
		cfg.DB.DSN = value
	}
	if value, ok := lookupEnv("JWT_SECRET"); ok {
		cfg.Jwt.Secret = value
	}
	if value, ok := lookupEnv("JWT_ISSUER"); ok {
		cfg.Jwt.Issuer = value
	}
	if value, ok := lookupEnv("AUTH_ALLOW_DEMO_USER"); ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid AUTH_ALLOW_DEMO_USER: %w", err)
		}
		cfg.Auth.AllowDemoUser = parsed
	}
	if value, ok := lookupEnv("AUTH_STATIC_TOKEN_ENABLED"); ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid AUTH_STATIC_TOKEN_ENABLED: %w", err)
		}
		cfg.Auth.StaticTokenEnabled = parsed
	}
	if value, ok := lookupEnv("AUTH_BOOTSTRAP_USERS_JSON"); ok {
		var users []BootstrapUser
		if err := json.Unmarshal([]byte(value), &users); err != nil {
			return fmt.Errorf("invalid AUTH_BOOTSTRAP_USERS_JSON: %w", err)
		}
		cfg.Auth.BootstrapUsers = users
	}
	if value, ok := lookupEnv("HTTP_ALLOWED_ORIGINS"); ok {
		cfg.HTTP.AllowedOrigins = splitCSV(value)
	}
	if value, ok := lookupEnv("HTTP_ALLOWED_CIDRS"); ok {
		cfg.HTTP.AllowedCIDRs = splitCSV(value)
	}
	if value, ok := lookupEnv("HTTP_TRUSTED_PROXY_CIDRS"); ok {
		cfg.HTTP.TrustedProxyCIDRs = splitCSV(value)
	}
	if value, ok := lookupEnv("HTTP_RATE_LIMIT_PER_MINUTE"); ok {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid HTTP_RATE_LIMIT_PER_MINUTE: %w", err)
		}
		cfg.HTTP.RateLimitPerMinute = parsed
	}
	if value, ok := lookupEnv("HTTP_LOG_TO_STDOUT"); ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid HTTP_LOG_TO_STDOUT: %w", err)
		}
		cfg.HTTP.LogToStdout = parsed
	}
	return nil
}

func lookupEnv(key string) (string, bool) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return "", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	return value, true
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}
