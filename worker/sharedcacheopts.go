package worker

import (
	"time"
)

// SharedCacheOptions tunes cross-daemon shared cache performance (postgres + S3).
type SharedCacheOptions struct {
	SyncUploadOnSave    bool
	UploadTopLayerOnly  bool
	PrefetchOnLoad      bool
	ExistsRetryAttempts int
	ExistsRetryInterval time.Duration
}

// DefaultSharedCacheOptions returns performance defaults (async upload, prefetch enabled).
func DefaultSharedCacheOptions() SharedCacheOptions {
	return SharedCacheOptions{
		SyncUploadOnSave:    false,
		UploadTopLayerOnly:  true,
		PrefetchOnLoad:      true,
		ExistsRetryAttempts: 5,
		ExistsRetryInterval: 2 * time.Second,
	}
}
