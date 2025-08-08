package memory

import (
	"github.com/ZanzyTHEbar/mcp-memory-libsql-go/internal/database"
)

// Config exposes a stable wrapper for database configuration in package mode.
// Most fields map directly to internal/database.Config.
type Config struct {
	URL                string
	AuthToken          string
	ProjectsDir        string
	MultiProjectMode   bool
	EmbeddingDims      int
	MaxOpenConns       int
	MaxIdleConns       int
	ConnMaxIdleSec     int
	ConnMaxLifeSec     int
	EmbeddingsProvider string
}

func (c *Config) toInternal() *database.Config {
	return &database.Config{
		URL:                c.URL,
		AuthToken:          c.AuthToken,
		ProjectsDir:        c.ProjectsDir,
		MultiProjectMode:   c.MultiProjectMode,
		EmbeddingDims:      c.EmbeddingDims,
		MaxOpenConns:       c.MaxOpenConns,
		MaxIdleConns:       c.MaxIdleConns,
		ConnMaxIdleSec:     c.ConnMaxIdleSec,
		ConnMaxLifeSec:     c.ConnMaxLifeSec,
		EmbeddingsProvider: c.EmbeddingsProvider,
	}
}
