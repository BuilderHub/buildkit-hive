package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateCache(t *testing.T) {
	t.Run("default bbolt", func(t *testing.T) {
		cfg := Config{}
		require.NoError(t, cfg.ValidateCache())
	})

	t.Run("postgres requires dsn", func(t *testing.T) {
		cfg := Config{Cache: CacheConfig{Backend: "postgres"}}
		require.Error(t, cfg.ValidateCache())
	})

	t.Run("postgres with dsn", func(t *testing.T) {
		cfg := Config{Cache: CacheConfig{
			Backend:     "postgres",
			PostgresDSN: "postgres://u:p@localhost/db",
		}}
		require.NoError(t, cfg.ValidateCache())
	})

	t.Run("s3 requires bucket and region", func(t *testing.T) {
		cfg := Config{Cache: CacheConfig{S3: &S3ContentStoreConfig{}}}
		require.Error(t, cfg.ValidateCache())
	})

	t.Run("s3 valid", func(t *testing.T) {
		cfg := Config{Cache: CacheConfig{S3: &S3ContentStoreConfig{
			Bucket: "b",
			Region: "us-east-1",
		}}}
		require.NoError(t, cfg.ValidateCache())
	})
}
