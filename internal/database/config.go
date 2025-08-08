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

	return &Config{
		URL:           url,
		AuthToken:     authToken,
		EmbeddingDims: dims,
	}
}
