package worker

import (
	"context"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	cacheconfig "github.com/moby/buildkit/cache/config"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/util/compression"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// ResultLookup loads stored cache result metadata by result ID.
type ResultLookup interface {
	GetResultByID(resultID string) (solver.CacheResult, error)
}

// NewSharedCacheResultStorage wraps cache result storage with cross-daemon rehydration support.
func NewSharedCacheResultStorage(wc *Controller, lookup ResultLookup) solver.CacheResultStorage {
	return &sharedCacheResultStorage{
		inner:  NewCacheResultStorage(wc),
		wc:     wc,
		lookup: lookup,
	}
}

type sharedCacheResultStorage struct {
	inner  solver.CacheResultStorage
	wc     *Controller
	lookup ResultLookup
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
	comp := compression.New(compression.Default)
	refCfg := cacheconfig.RefConfig{Compression: comp}
	remotes, err := wref.GetRemotes(context.TODO(), false, refCfg, false, nil)
	if err != nil || len(remotes) == 0 {
		return cr, nil
	}
	cr.Descriptors = append([]ocispecs.Descriptor(nil), remotes[0].Descriptors...)
	return cr, nil
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
	cr := res
	if len(cr.Descriptors) == 0 && s.lookup != nil {
		if looked, err := s.lookup.GetResultByID(res.ID); err == nil {
			cr = looked
		}
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
	return descriptorsAvailable(ctx, w.ContentStore(), cr.Descriptors)
}

func (s *sharedCacheResultStorage) loadFromDescriptors(ctx context.Context, res solver.CacheResult) (solver.Result, error) {
	cr := res
	if len(cr.Descriptors) == 0 && s.lookup != nil {
		var err error
		cr, err = s.lookup.GetResultByID(res.ID)
		if err != nil {
			return nil, err
		}
	}
	if len(cr.Descriptors) == 0 {
		return nil, errors.WithStack(solver.ErrNotFound)
	}
	w, err := s.wc.GetDefault()
	if err != nil {
		return nil, err
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

func descriptorsAvailable(ctx context.Context, store content.InfoProvider, descs []ocispecs.Descriptor) bool {
	for _, desc := range descs {
		if _, err := store.Info(ctx, desc.Digest); err != nil {
			return false
		}
	}
	return len(descs) > 0
}
