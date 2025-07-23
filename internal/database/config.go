package database

import (
	"os"
)

// Config holds the database configuration
type Config struct {
	URL              string
	AuthToken        string
	ProjectsDir      string
	MultiProjectMode bool
}

// NewConfig creates a new Config from environment variables
func NewConfig() *Config {
	url := os.Getenv("LIBSQL_URL")
	if url == "" {
		url = "file:./libsql.db"
	}

	authToken := os.Getenv("LIBSQL_AUTH_TOKEN")

	return &Config{
		URL:       url,
		AuthToken: authToken,
	}
}
