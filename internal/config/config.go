package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration structure loaded from config.yaml
type Config struct {
	Server   ServerConfig             `yaml:"server"`
	Auth     AuthConfig               `yaml:"auth"`
	JWT      JWTConfig                `yaml:"jwt"`
	TLS      TLSConfig                `yaml:"tls"`
	Routes   []RouteConfig            `yaml:"routes"`
	Services map[string]ServiceConfig  `yaml:"services"`
	Redis    RedisConfig              `yaml:"redis"`
	Postgres PostgresConfig           `yaml:"postgres"`
}

type ServerConfig struct {
	Port         int           `yaml:"port"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
	IdleTimeout  time.Duration `yaml:"idle_timeout"`
}

type AuthConfig struct {
	Enabled     bool     `yaml:"enabled"`
	ValidTokens []string `yaml:"valid_tokens"`
}

// JWTConfig holds settings for RS256 JWT validation via JWKS.
type JWTConfig struct {
	// Enabled switches the auth middleware from static tokens to JWT validation.
	Enabled  bool   `yaml:"enabled"`
	// JWKSURL is the public key endpoint, e.g. https://your-auth/.well-known/jwks.json
	JWKSURL  string `yaml:"jwks_url"`
	// Issuer is the expected value of the `iss` claim in the JWT.
	Issuer   string `yaml:"issuer"`
	// Audience is the expected value of the `aud` claim in the JWT.
	Audience string `yaml:"audience"`
}

// TLSConfig holds settings for mutual TLS to upstream backends.
type TLSConfig struct {
	// Enabled switches the proxy transport from plain HTTP to mTLS.
	Enabled    bool   `yaml:"enabled"`
	// CACert is the path to the CA cert used to verify backend server certificates.
	CACert     string `yaml:"ca_cert"`
	// ClientCert is the path to the gateway's own client certificate.
	ClientCert string `yaml:"client_cert"`
	// ClientKey is the path to the gateway's own private key.
	ClientKey  string `yaml:"client_key"`
}

type RouteConfig struct {
	Path      string          `yaml:"path"`
	Service   string          `yaml:"service"`
	Timeout   time.Duration   `yaml:"timeout"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
}

type RateLimitConfig struct {
	RequestsPerMinute int `yaml:"requests_per_minute"`
}

type ServiceConfig struct {
	Upstreams        []string          `yaml:"upstreams"`
	HealthCheck      HealthCheckConfig `yaml:"health_check"`
	BalanceStrategy  string            `yaml:"balance_strategy"`
}

type HealthCheckConfig struct {
	Path     string        `yaml:"path"`
	Interval time.Duration `yaml:"interval"`
	Timeout  time.Duration `yaml:"timeout"`
}

type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type PostgresConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbname"`
	SSLMode  string `yaml:"sslmode"`
}

// DSN returns the PostgreSQL connection string
func (p PostgresConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		p.Host, p.Port, p.User, p.Password, p.DBName, p.SSLMode,
	)
}

// Load reads and parses the config YAML file at the given path
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	setDefaults(&cfg)
	return &cfg, nil
}

func setDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.ReadTimeout == 0 {
		cfg.Server.ReadTimeout = 30 * time.Second
	}
	if cfg.Server.WriteTimeout == 0 {
		cfg.Server.WriteTimeout = 30 * time.Second
	}
	if cfg.Server.IdleTimeout == 0 {
		cfg.Server.IdleTimeout = 120 * time.Second
	}
}
