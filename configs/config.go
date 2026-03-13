package configs

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"

	"github.com/Royaltyprogram/aiops/pkg/xslog"
)

type Config struct {
	App    App          `koanf:"App"`
	DB     DB           `koanf:"DB"`
	Redis  Redis        `koanf:"Redis"`
	Log    xslog.Config `koanf:"Log"`
	Jwt    Jwt          `koanf:"Jwt"`
	Auth   Auth         `koanf:"Auth"`
	HTTP   HTTP         `koanf:"HTTP"`
	OpenAI OpenAI       `koanf:"OpenAI"`
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
	Role     string `json:"role" koanf:"Role"`
	Password string `json:"password" koanf:"Password"`
}

type HTTP struct {
	AllowedOrigins     []string `koanf:"AllowedOrigins"`
	AllowedCIDRs       []string `koanf:"AllowedCIDRs"`
	AdminAllowedCIDRs  []string `koanf:"AdminAllowedCIDRs"`
	TrustedProxyCIDRs  []string `koanf:"TrustedProxyCIDRs"`
	RateLimitPerMinute int      `koanf:"RateLimitPerMinute"`
	LogToStdout        bool     `koanf:"LogToStdout"`
}

type OpenAI struct {
	APIKey              string `koanf:"APIKey"`
	BaseURL             string `koanf:"BaseURL"`
	ResponsesModel      string `koanf:"ResponsesModel"`
	ReportPromptVariant string `koanf:"ReportPromptVariant"`
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
		Prefix:        "",
		TransformFunc: configEnvTransform,
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
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) Validate() error {
	issues := make([]string, 0)

	switch strings.TrimSpace(c.DB.Dialect) {
	case "sqlite3", "mysql":
	case "":
		issues = append(issues, "DB.Dialect is required")
	default:
		issues = append(issues, fmt.Sprintf("DB.Dialect %q is unsupported", c.DB.Dialect))
	}
	if strings.TrimSpace(c.DB.DSN) == "" {
		issues = append(issues, "DB.DSN is required")
	}
	if c.HTTP.RateLimitPerMinute < 0 {
		issues = append(issues, "HTTP.RateLimitPerMinute must be zero or greater")
	}
	issues = append(issues, validateCIDRList("HTTP.AllowedCIDRs", c.HTTP.AllowedCIDRs)...)
	issues = append(issues, validateCIDRList("HTTP.AdminAllowedCIDRs", c.HTTP.AdminAllowedCIDRs)...)
	issues = append(issues, validateCIDRList("HTTP.TrustedProxyCIDRs", c.HTTP.TrustedProxyCIDRs)...)
	issues = append(issues, validateBootstrapUsers(c.Auth.BootstrapUsers)...)

	if c.Auth.StaticTokenEnabled && strings.TrimSpace(c.App.APIToken) == "" {
		issues = append(issues, "App.APIToken is required when Auth.StaticTokenEnabled is true")
	}
	if c.IsReleaseMode() {
		if strings.TrimSpace(c.Jwt.Secret) == "" {
			issues = append(issues, "Jwt.Secret is required in release mode")
		}
		if c.Auth.AllowDemoUser {
			issues = append(issues, "Auth.AllowDemoUser must be false in release mode")
		}
		if c.Auth.StaticTokenEnabled {
			issues = append(issues, "Auth.StaticTokenEnabled must be false in release mode")
		}
	}

	if len(issues) > 0 {
		return fmt.Errorf("invalid config: %s", strings.Join(issues, "; "))
	}
	return nil
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
	if value, ok := lookupEnv("APP_API_TOKEN_FILE"); ok {
		secret, err := readSecretFile(value)
		if err != nil {
			return fmt.Errorf("invalid APP_API_TOKEN_FILE: %w", err)
		}
		cfg.App.APIToken = secret
	}
	if value, ok := lookupEnv("DB_DIALECT"); ok {
		cfg.DB.Dialect = value
	}
	if value, ok := lookupEnv("DB_DSN"); ok {
		cfg.DB.DSN = value
	}
	if value, ok := lookupEnv("DB_DSN_FILE"); ok {
		secret, err := readSecretFile(value)
		if err != nil {
			return fmt.Errorf("invalid DB_DSN_FILE: %w", err)
		}
		cfg.DB.DSN = secret
	}
	if value, ok := lookupEnv("JWT_SECRET"); ok {
		cfg.Jwt.Secret = value
	}
	if value, ok := lookupEnv("JWT_SECRET_FILE"); ok {
		secret, err := readSecretFile(value)
		if err != nil {
			return fmt.Errorf("invalid JWT_SECRET_FILE: %w", err)
		}
		cfg.Jwt.Secret = secret
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
	if value, ok := lookupEnv("AUTH_BOOTSTRAP_USERS_FILE"); ok {
		raw, err := os.ReadFile(value)
		if err != nil {
			return fmt.Errorf("invalid AUTH_BOOTSTRAP_USERS_FILE: %w", err)
		}
		var users []BootstrapUser
		if err := json.Unmarshal(raw, &users); err != nil {
			return fmt.Errorf("invalid AUTH_BOOTSTRAP_USERS_FILE: %w", err)
		}
		cfg.Auth.BootstrapUsers = users
	}
	if value, ok := lookupEnv("HTTP_ALLOWED_ORIGINS"); ok {
		cfg.HTTP.AllowedOrigins = splitCSV(value)
	}
	if value, ok := lookupEnv("HTTP_ALLOWED_CIDRS"); ok {
		cfg.HTTP.AllowedCIDRs = splitCSV(value)
	}
	if value, ok := lookupEnv("HTTP_ADMIN_ALLOWED_CIDRS"); ok {
		cfg.HTTP.AdminAllowedCIDRs = splitCSV(value)
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
	if value, ok := lookupEnv("OPENAI_API_KEY"); ok {
		cfg.OpenAI.APIKey = value
	}
	if value, ok := lookupEnv("OPENAI_API_KEY_FILE"); ok {
		secret, err := readSecretFile(value)
		if err != nil {
			return fmt.Errorf("invalid OPENAI_API_KEY_FILE: %w", err)
		}
		cfg.OpenAI.APIKey = secret
	}
	if value, ok := lookupEnv("OPENAI_BASE_URL"); ok {
		cfg.OpenAI.BaseURL = value
	}
	if value, ok := lookupEnv("OPENAI_RESPONSES_MODEL"); ok {
		cfg.OpenAI.ResponsesModel = value
	}
	if value, ok := lookupEnv("OPENAI_REPORT_PROMPT_VARIANT"); ok {
		cfg.OpenAI.ReportPromptVariant = value
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

func configEnvTransform(key, value string) (string, any) {
	if strings.HasSuffix(key, "_FILE") {
		return "", nil
	}
	return key, value
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

func readSecretFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", fmt.Errorf("file is empty")
	}
	return value, nil
}

func validateCIDRList(label string, values []string) []string {
	issues := make([]string, 0)
	for _, value := range values {
		if _, _, err := net.ParseCIDR(value); err != nil {
			issues = append(issues, fmt.Sprintf("%s contains invalid CIDR %q", label, value))
		}
	}
	return issues
}

func validateBootstrapUsers(users []BootstrapUser) []string {
	issues := make([]string, 0)
	seenIDs := make(map[string]struct{}, len(users))
	seenEmails := make(map[string]struct{}, len(users))

	for i, user := range users {
		prefix := fmt.Sprintf("Auth.BootstrapUsers[%d]", i)
		id := strings.TrimSpace(user.ID)
		orgID := strings.TrimSpace(user.OrgID)
		orgName := strings.TrimSpace(user.OrgName)
		email := strings.TrimSpace(user.Email)
		name := strings.TrimSpace(user.Name)
		password := strings.TrimSpace(user.Password)

		if id == "" {
			issues = append(issues, prefix+".ID is required")
		}
		if orgID == "" {
			issues = append(issues, prefix+".OrgID is required")
		}
		if orgName == "" {
			issues = append(issues, prefix+".OrgName is required")
		}
		if email == "" {
			issues = append(issues, prefix+".Email is required")
		}
		if name == "" {
			issues = append(issues, prefix+".Name is required")
		}
		if role := normalizeBootstrapUserRole(user.Role); role == "" {
			issues = append(issues, prefix+".Role must be one of: admin, member")
		}
		if password == "" {
			issues = append(issues, prefix+".Password is required")
		}

		if id != "" {
			if _, exists := seenIDs[id]; exists {
				issues = append(issues, prefix+".ID must be unique")
			}
			seenIDs[id] = struct{}{}
		}
		if email != "" {
			emailKey := strings.ToLower(email)
			if _, exists := seenEmails[emailKey]; exists {
				issues = append(issues, prefix+".Email must be unique")
			}
			seenEmails[emailKey] = struct{}{}
		}
	}

	return issues
}

func normalizeBootstrapUserRole(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "member":
		return "member"
	case "admin":
		return "admin"
	default:
		return ""
	}
}
