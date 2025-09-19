package config

import (
	"fmt"
	"os"
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

	// LLM / Eino configuration (engine-owned)
	LLMProvider      string
	GeminiAPIKey     string
	DefaultLLMModel  string
	FallbackLLMModel string
}

func getenv(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}

func Load() Config {
	cfg := Config{
		AppEnv:        getenv("APP_ENV", "development"),
		HTTPAddr:      getenv("HTTP_ADDR", ":8081"),
		RedisAddr:     getenv("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPassword: os.Getenv("REDIS_PASSWORD"),
		DataDir:       getenv("DATA_DIR", "./data"),

		SupabaseURL:        os.Getenv("NEXT_PUBLIC_SUPABASE_URL"),
		SupabaseServiceKey: os.Getenv("SUPABASE_SERVICE_ROLE_KEY"),
		SupabaseBucket:     getenv("SUPABASE_STORAGE_BUCKET", "screenshots"),

		LLMProvider:      getenv("LLM_PROVIDER", "gemini"),
		GeminiAPIKey:     os.Getenv("GEMINI_API_KEY"),
		DefaultLLMModel:  getenv("DEFAULT_LLM_MODEL", "gemini-1.5-flash"),
		FallbackLLMModel: getenv("FALLBACK_LLM_MODEL", "gemini-1.5-pro"),
	}
	if cfg.RedisAddr == "" {
		panic(fmt.Errorf("REDIS_ADDR is required"))
	}
	return cfg
}
