package worker

import (
	"context"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	cacheconfig "github.com/moby/buildkit/cache/config"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/compression"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// ResultLookup loads stored cache result metadata by result ID.
type ResultLookup interface {
	GetResultByID(resultID string) (solver.CacheResult, error)
}

type sharedBlobStore interface {
	EnsureUploaded(context.Context, []ocispecs.Descriptor, bool) error
	EnsureLocal(context.Context, []ocispecs.Descriptor) error
}

// NewSharedCacheResultStorage wraps cache result storage with cross-daemon rehydration support.
func NewSharedCacheResultStorage(wc *Controller, lookup ResultLookup, opts SharedCacheOptions) solver.CacheResultStorage {
	if opts.ExistsRetryAttempts < 1 {
		opts.ExistsRetryAttempts = 1
	}
	if opts.ExistsRetryInterval <= 0 {
		opts.ExistsRetryInterval = 2 * time.Second
	}
	return &sharedCacheResultStorage{
		inner:  NewCacheResultStorage(wc),
		wc:     wc,
		lookup: lookup,
		opts:   opts,
	}
}

type sharedCacheResultStorage struct {
	inner  solver.CacheResultStorage
	wc     *Controller
	lookup ResultLookup
	opts   SharedCacheOptions
}

func (s *sharedCacheResultStorage) Save(res solver.Result, createdAt time.Time) (solver.CacheResult, error) {
	cr, err := s.inner.Save(res, createdAt)
	if err != nil {
		return cr, err
	}
	wref, ok := res.Sys().(*WorkerRef)
	if !ok || wref == nil {
		return cr, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	comp := compression.New(compression.Default)
	refCfg := cacheconfig.RefConfig{Compression: comp}
	remotes, err := wref.GetRemotes(ctx, true, refCfg, false, nil)
	if err != nil {
		bklog.L.WithError(err).Warn("failed to export cache result descriptors for shared storage")
		return cr, nil
	}
	if len(remotes) == 0 || len(remotes[0].Descriptors) == 0 {
		bklog.L.Warn("no exportable descriptors for shared cache result; cross-daemon reuse unavailable")
		return cr, nil
	}
	cr.Descriptors = append([]ocispecs.Descriptor(nil), remotes[0].Descriptors...)

	if s.opts.SyncUploadOnSave {
		if err := ensureSharedBlobsUploaded(ctx, wref.Worker, cr.Descriptors, s.opts.UploadTopLayerOnly); err != nil {
			return cr, errors.Wrap(err, "failed to upload shared cache blobs to S3")
		}
	}
	return cr, nil
}

func ensureSharedBlobsUploaded(ctx context.Context, w Worker, descs []ocispecs.Descriptor, topLayerOnly bool) error {
	cs := w.ContentStore()
	if cs == nil {
		return nil
	}
	return ensureUploaded(ctx, cs.Store, descs, topLayerOnly)
}

func ensureSharedBlobsLocal(ctx context.Context, w Worker, descs []ocispecs.Descriptor) error {
	cs := w.ContentStore()
	if cs == nil {
		return nil
	}
	return ensureLocal(ctx, cs.Store, descs)
}

func ensureUploaded(ctx context.Context, store content.Store, descs []ocispecs.Descriptor, topLayerOnly bool) error {
	if len(descs) == 0 {
		return nil
	}
	u, ok := store.(sharedBlobStore)
	if !ok {
		return nil
	}
	return u.EnsureUploaded(ctx, descs, topLayerOnly)
}

func ensureLocal(ctx context.Context, store content.Store, descs []ocispecs.Descriptor) error {
	if len(descs) == 0 {
		return nil
	}
	u, ok := store.(sharedBlobStore)
	if !ok {
		return nil
	}
	return u.EnsureLocal(ctx, descs)
}

func (s *sharedCacheResultStorage) Load(ctx context.Context, res solver.CacheResult) (solver.Result, error) {
	r, err := s.inner.Load(ctx, res)
	if err == nil {
		return r, nil
	}
	return s.loadFromDescriptors(ctx, res)
}

func (s *sharedCacheResultStorage) LoadRemotes(ctx context.Context, res solver.CacheResult, compressionopt *compression.Config, g session.Group) ([]*solver.Remote, error) {
	remotes, err := s.inner.LoadRemotes(ctx, res, compressionopt, g)
	if err == nil && len(remotes) > 0 {
		return remotes, nil
	}
	cr, err := s.resolveCacheResult(res)
	if err != nil {
		return nil, err
	}
	if len(cr.Descriptors) == 0 {
		return nil, errors.WithStack(solver.ErrNotFound)
	}
	w, err := s.wc.GetDefault()
	if err != nil {
		return nil, err
	}
	provider := w.ContentStore()
	return []*solver.Remote{{
		Descriptors: cr.Descriptors,
		Provider:    provider,
	}}, nil
}

func (s *sharedCacheResultStorage) Exists(ctx context.Context, id string) bool {
	if s.inner.Exists(ctx, id) {
		return true
	}
	if s.lookup == nil {
		return false
	}
	cr, err := s.lookup.GetResultByID(id)
	if err != nil || len(cr.Descriptors) == 0 {
		return false
	}
	w, err := s.wc.GetDefault()
	if err != nil {
		return false
	}
	return descriptorsAvailableWithRetry(ctx, w.ContentStore(), cr.Descriptors, s.opts.ExistsRetryAttempts, s.opts.ExistsRetryInterval)
}

func (s *sharedCacheResultStorage) loadFromDescriptors(ctx context.Context, res solver.CacheResult) (solver.Result, error) {
	cr, err := s.resolveCacheResult(res)
	if err != nil {
		return nil, err
	}
	if len(cr.Descriptors) == 0 {
		return nil, errors.WithStack(solver.ErrNotFound)
	}
	w, err := s.wc.GetDefault()
	if err != nil {
		return nil, err
	}
	if s.opts.PrefetchOnLoad {
		if err := ensureSharedBlobsLocal(ctx, w, cr.Descriptors); err != nil {
			return nil, errors.Wrap(err, "failed to prefetch shared cache blobs from S3")
		}
	}
	ref, err := w.FromRemote(ctx, &solver.Remote{
		Descriptors: cr.Descriptors,
		Provider:    w.ContentStore(),
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to rehydrate cache result from shared store")
	}
	return NewWorkerRefResult(ref, w), nil
}

func (s *sharedCacheResultStorage) resolveCacheResult(res solver.CacheResult) (solver.CacheResult, error) {
	if len(res.Descriptors) > 0 {
		return res, nil
	}
	if s.lookup == nil {
		return res, nil
	}
	return s.lookup.GetResultByID(res.ID)
}

func descriptorsAvailable(ctx context.Context, store content.InfoProvider, descs []ocispecs.Descriptor) bool {
	for _, desc := range descs {
		if _, err := store.Info(ctx, desc.Digest); err != nil {
			return false
		}
	}
	return len(descs) > 0
}

func descriptorsAvailableWithRetry(ctx context.Context, store content.InfoProvider, descs []ocispecs.Descriptor, attempts int, interval time.Duration) bool {
	if attempts < 1 {
		attempts = 1
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	for i := 0; i < attempts; i++ {
		if descriptorsAvailable(ctx, store, descs) {
			return true
		}
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(interval):
		}
	}
	return false
}
