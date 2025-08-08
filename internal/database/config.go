package database

import (
	"os"
	"strconv"
)

// Config holds the database configuration
type Config struct {
	URL              string
	AuthToken        string
	ProjectsDir      string
	MultiProjectMode bool
	EmbeddingDims    int
	MaxOpenConns     int
	MaxIdleConns     int
	ConnMaxIdleSec   int
	ConnMaxLifeSec   int
	// Embeddings provider hints (optional)
	EmbeddingsProvider string // e.g., "openai", "ollama"
}

// NewConfig creates a new Config from environment variables
func NewConfig() *Config {
	url := os.Getenv("LIBSQL_URL")
	if url == "" {
		url = "file:./libsql.db"
	}

	authToken := os.Getenv("LIBSQL_AUTH_TOKEN")
	dims := 4
	if v := os.Getenv("EMBEDDING_DIMS"); v != "" {
		// simple parse, ignore error -> keep default
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			dims = n
		}
	}

	maxOpen := 0
	if v := os.Getenv("DB_MAX_OPEN_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			maxOpen = n
		}
	}
	maxIdle := 0
	if v := os.Getenv("DB_MAX_IDLE_CONNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			maxIdle = n
		}
	}
	idleSec := 0
	if v := os.Getenv("DB_CONN_MAX_IDLE_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			idleSec = n
		}
	}
	lifeSec := 0
	if v := os.Getenv("DB_CONN_MAX_LIFETIME_SEC"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			lifeSec = n
		}
	}

	return &Config{
		URL:            url,
		AuthToken:      authToken,
		EmbeddingDims:  dims,
		MaxOpenConns:   maxOpen,
		MaxIdleConns:   maxIdle,
		ConnMaxIdleSec: idleSec,
		ConnMaxLifeSec: lifeSec,
	}
}
