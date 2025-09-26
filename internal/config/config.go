package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	AppEnv        string
	HTTPAddr      string
	RedisAddr     string
	RedisPassword string
	DataDir       string

	SupabaseURL        string
	SupabaseServiceKey string
	SupabaseBucket     string

	LLMProvider      string
	GeminiAPIKey     string
	DefaultLLMModel  string
	FallbackLLMModel string

	TaskMaxRetries int

	// System auth for backend webhooks
	SystemAuthSecret string
}

func getenv(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}

func getenvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}

// getenvOrFile reads from environment variable or file if _FILE suffix is present
func getenvOrFile(key string) string {
	// Check for direct env var first
	if val := os.Getenv(key); val != "" {
		return val
	}

	// Check for file-based env var
	fileKey := key + "_FILE"
	if filePath := os.Getenv(fileKey); filePath != "" {
		content, err := ioutil.ReadFile(filePath)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(content))
	}

	return ""
}

func Load() Config {
	cfg := Config{
		AppEnv:        getenv("APP_ENV", "development"),
		HTTPAddr:      getenv("HTTP_ADDR", ":8081"),
		RedisAddr:     getenv("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
		DataDir:       getenv("DATA_DIR", "./data"),

		SupabaseURL:        os.Getenv("NEXT_PUBLIC_SUPABASE_URL"),
		SupabaseServiceKey: getenvOrFile("SUPABASE_SERVICE_ROLE_KEY"),
		SupabaseBucket:     getenv("SUPABASE_STORAGE_BUCKET", "screenshots"),

		LLMProvider:      getenv("LLM_PROVIDER", "gemini"),
		GeminiAPIKey:     getenvOrFile("GEMINI_API_KEY"),
		DefaultLLMModel:  getenv("DEFAULT_LLM_MODEL", "gemini-1.5-flash"),
		FallbackLLMModel: getenv("FALLBACK_LLM_MODEL", "gemini-1.5-pro"),

		TaskMaxRetries: getenvInt("TASK_MAX_RETRIES", 3),

		SystemAuthSecret: getenvOrFile("SYSTEM_AUTH_SECRET"),
	}
	if cfg.RedisAddr == "" {
		panic(fmt.Errorf("REDIS_ADDR is required"))
	}
	return cfg
}
