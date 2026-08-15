package config

import (
	"log"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Server    ServerConfig
	Logging   LoggingConfig
	S3        S3Config
	Database  DatabaseConfig
	Auth      AuthConfig
	Retention RetentionConfig
}

// RetentionConfig sets how long uploads are kept. Hours so tests can shrink it.
type RetentionConfig struct {
	AnonHours  int
	OwnedHours int
}

type ServerConfig struct {
	Port            int
	ReadTimeout     int
	WriteTimeout    int
	IdleTimeout     int
	ShutdownTimeout int
	MaxUploadMB     int64
	AllowedOrigins  []string
	TrustedProxies  []string
}

type LoggingConfig struct {
	Level string
	Mode  string
}

type S3Config struct {
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	UseSSL          bool
	BucketName      string
	ForcePathStyle  bool
}

type DatabaseConfig struct {
	URL string
}

type AuthConfig struct {
	JWKSURL  string
	Issuer   string
	Audience string
}

func Load() *Config {
	cfg := &Config{
		Server: ServerConfig{
			Port:            getEnvInt("PORT", 6060),
			ReadTimeout:     getEnvInt("READ_TIMEOUT", 30),
			WriteTimeout:    getEnvInt("WRITE_TIMEOUT", 30),
			IdleTimeout:     getEnvInt("IDLE_TIMEOUT", 120),
			ShutdownTimeout: getEnvInt("SHUTDOWN_TIMEOUT", 5),
			MaxUploadMB:     int64(getEnvInt("MAX_UPLOAD_MB", 512)),
			AllowedOrigins:  getEnvList("ALLOWED_ORIGINS", []string{"*"}),
			TrustedProxies:  getEnvList("TRUSTED_PROXIES", nil),
		},
		Logging: LoggingConfig{
			Level: getEnv("LOG_LEVEL", "info"),
			Mode:  getEnv("GIN_MODE", "release"),
		},
		S3: S3Config{
			Endpoint:        getEnv("S3_ENDPOINT", ""),
			Region:          getEnv("S3_REGION", "us-east-1"),
			AccessKeyID:     getEnv("S3_ACCESS_KEY_ID", ""),
			SecretAccessKey: getEnv("S3_SECRET_ACCESS_KEY", ""),
			UseSSL:          getEnvBool("S3_USE_SSL", true),
			BucketName:      getEnv("S3_BUCKET_NAME", "oss-archive"),
			ForcePathStyle:  getEnvBool("S3_FORCE_PATH_STYLE", false),
		},
		Database: DatabaseConfig{
			URL: getEnv("DATABASE_URL", ""),
		},
		Auth: AuthConfig{
			JWKSURL:  getEnv("AUTH_JWKS_URL", ""),
			Issuer:   getEnv("AUTH_ISSUER", ""),
			Audience: getEnv("AUTH_AUDIENCE", ""),
		},
		Retention: RetentionConfig{
			AnonHours:  getEnvInt("RETENTION_ANON_HOURS", 72),
			OwnedHours: getEnvInt("RETENTION_OWNED_HOURS", 168),
		},
	}

	log.Printf("config: port=%d s3_endpoint=%q s3_region=%s s3_bucket=%s s3_access_key=%s",
		cfg.Server.Port, cfg.S3.Endpoint, cfg.S3.Region, cfg.S3.BucketName, mask(cfg.S3.AccessKeyID))

	return cfg
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvList(key string, defaultValue []string) []string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	parts := strings.Split(value, ",")
	list := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			list = append(list, trimmed)
		}
	}
	if len(list) == 0 {
		return defaultValue
	}
	return list
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}

func mask(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:4] + "****"
}
