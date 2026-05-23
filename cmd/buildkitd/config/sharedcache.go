package config

import (
	"time"

	"github.com/pkg/errors"
)

const (
	defaultUploadParallelism   = 4
	defaultExistsRetryAsync    = 5
	defaultExistsRetrySync     = 1
	defaultExistsRetryInterval = 2 * time.Second
)

// SharedCachePerformance holds resolved shared-cache tuning for postgres+S3.
type SharedCachePerformance struct {
	SyncUploadOnSave    bool
	UploadTopLayerOnly  bool
	PrefetchOnLoad      bool
	ExistsRetryAttempts int
	ExistsRetryInterval time.Duration
}

func (c *S3ContentStoreConfig) SharedCachePerformance() SharedCachePerformance {
	if c == nil {
		return SharedCachePerformance{
			SyncUploadOnSave:    false,
			UploadTopLayerOnly:  true,
			PrefetchOnLoad:      true,
			ExistsRetryAttempts: defaultExistsRetryAsync,
			ExistsRetryInterval: defaultExistsRetryInterval,
		}
	}
	syncUpload := false
	if c.SyncUploadOnSave != nil {
		syncUpload = *c.SyncUploadOnSave
	}
	topLayerOnly := true
	if c.UploadTopLayerOnly != nil {
		topLayerOnly = *c.UploadTopLayerOnly
	}
	prefetch := true
	if c.PrefetchOnLoad != nil {
		prefetch = *c.PrefetchOnLoad
	}
	attempts := defaultExistsRetryAsync
	if syncUpload {
		attempts = defaultExistsRetrySync
	}
	if c.ExistsRetryAttempts != nil {
		attempts = *c.ExistsRetryAttempts
	}
	interval := defaultExistsRetryInterval
	if c.ExistsRetryInterval != nil && c.ExistsRetryInterval.Duration > 0 {
		interval = c.ExistsRetryInterval.Duration
	}
	return SharedCachePerformance{
		SyncUploadOnSave:    syncUpload,
		UploadTopLayerOnly:  topLayerOnly,
		PrefetchOnLoad:      prefetch,
		ExistsRetryAttempts: attempts,
		ExistsRetryInterval: interval,
	}
}

func (c *S3ContentStoreConfig) resolvedUploadParallelism() int {
	if c == nil || c.UploadParallelism <= 0 {
		return defaultUploadParallelism
	}
	return c.UploadParallelism
}

func (c *S3ContentStoreConfig) validatePerformance() error {
	if c == nil {
		return nil
	}
	if c.UploadParallelism < 0 {
		return errors.New("cache.s3.uploadParallelism must be >= 0")
	}
	if c.ExistsRetryAttempts != nil && *c.ExistsRetryAttempts < 1 {
		return errors.New("cache.s3.existsRetryAttempts must be >= 1")
	}
	return nil
}
