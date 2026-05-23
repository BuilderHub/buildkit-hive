package config

import (
	"testing"
	"time"

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

	t.Run("s3 group default global", func(t *testing.T) {
		group, err := CacheConfig{S3: &S3ContentStoreConfig{
			Bucket: "b",
			Region: "us-east-1",
		}}.CacheGroup()
		require.NoError(t, err)
		require.Equal(t, "global", group)

		s3cfg, err := (&S3ContentStoreConfig{Bucket: "b", Region: "us-east-1"}).ToS3Config()
		require.NoError(t, err)
		require.Equal(t, "global", s3cfg.Group)
	})

	t.Run("s3 group custom", func(t *testing.T) {
		s3cfg, err := (&S3ContentStoreConfig{
			Bucket: "b",
			Region: "us-east-1",
			Group:  "team-x",
		}).ToS3Config()
		require.NoError(t, err)
		require.Equal(t, "team-x", s3cfg.Group)
	})

	t.Run("s3 invalid group", func(t *testing.T) {
		cfg := Config{Cache: CacheConfig{S3: &S3ContentStoreConfig{
			Bucket: "b",
			Region: "us-east-1",
			Group:  "team/x",
		}}}
		require.Error(t, cfg.ValidateCache())
	})

	t.Run("s3 from env", func(t *testing.T) {
		t.Setenv("AWS_BUCKET", "my-bucket")
		t.Setenv("AWS_REGION", "auto")
		t.Setenv("AWS_ENDPOINT_URL", "https://example.r2.cloudflarestorage.com")
		t.Setenv("AWS_ACCESS_KEY_ID", "key")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
		cfg, err := (&S3ContentStoreConfig{UsePathStyle: true}).ToS3Config()
		require.NoError(t, err)
		require.Equal(t, "my-bucket", cfg.Bucket)
		require.Equal(t, "auto", cfg.Region)
		require.Equal(t, "https://example.r2.cloudflarestorage.com", cfg.EndpointURL)
		require.True(t, cfg.UsePathStyle)
	})

	t.Run("shared cache performance defaults", func(t *testing.T) {
		perf := (&S3ContentStoreConfig{}).SharedCachePerformance()
		require.False(t, perf.SyncUploadOnSave)
		require.True(t, perf.UploadTopLayerOnly)
		require.True(t, perf.PrefetchOnLoad)
		require.Equal(t, 5, perf.ExistsRetryAttempts)
		require.Equal(t, 2*time.Second, perf.ExistsRetryInterval)
	})

	t.Run("shared cache sync upload retry", func(t *testing.T) {
		sync := true
		perf := (&S3ContentStoreConfig{SyncUploadOnSave: &sync}).SharedCachePerformance()
		require.True(t, perf.SyncUploadOnSave)
		require.Equal(t, 1, perf.ExistsRetryAttempts)
	})

	t.Run("upload parallelism in s3 config", func(t *testing.T) {
		cfg, err := (&S3ContentStoreConfig{
			Bucket:            "b",
			Region:            "us-east-1",
			UploadParallelism: 8,
		}).ToS3Config()
		require.NoError(t, err)
		require.Equal(t, 8, cfg.UploadParallelism)
	})
}
