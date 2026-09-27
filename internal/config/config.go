// Package config loads runtime configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all runtime settings for the API and seeder.
type Config struct {
	Port           string
	OpenSearchURL  string
	Username       string
	Password       string
	IndexPrefix    string
	SchemaDir      string
	TrackTotalHits string // "true", "false" or an integer threshold
	EmbedProvider  string
	EmbedDims      int
	OllamaURL      string
	OllamaModel    string
	LogBodies      bool
	CORSOrigins    []string
}

// Load reads .env (if present) and the process environment.
func Load() (Config, error) {
	_ = godotenv.Load()
	c := Config{
		Port:           get("PORT", "8080"),
		OpenSearchURL:  get("OS_URL", "http://localhost:9200"),
		Username:       os.Getenv("OS_USERNAME"),
		Password:       os.Getenv("OS_PASSWORD"),
		IndexPrefix:    get("OS_INDEX_PREFIX", "ds_"),
		SchemaDir:      get("SCHEMA_DIR", "./schemas"),
		TrackTotalHits: get("TRACK_TOTAL_HITS", "true"),
		EmbedProvider:  get("EMBED_PROVIDER", "fake"),
		OllamaURL:      get("OLLAMA_URL", "http://localhost:11434"),
		OllamaModel:    get("OLLAMA_MODEL", "nomic-embed-text"),
		LogBodies:      get("LOG_BODIES", "false") == "true",
		CORSOrigins:    strings.Split(get("CORS_ORIGINS", "http://localhost:3000"), ","),
	}
	dims, err := strconv.Atoi(get("EMBED_DIMS", "384"))
	if err != nil {
		return c, fmt.Errorf("EMBED_DIMS: %w", err)
	}
	c.EmbedDims = dims
	if c.IndexPrefix == "" {
		return c, fmt.Errorf("OS_INDEX_PREFIX must not be empty")
	}
	return c, nil
}

func get(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
