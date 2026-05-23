package worker

import (
	"context"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/buildkit/solver"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

type fakeInfoProvider struct {
	calls map[digest.Digest]int
	ready map[digest.Digest]bool
}

func (f *fakeInfoProvider) Info(_ context.Context, dgst digest.Digest) (content.Info, error) {
	f.calls[dgst]++
	if f.ready[dgst] {
		return content.Info{Digest: dgst}, nil
	}
	return content.Info{}, cerrdefs.ErrNotFound
}

func TestDescriptorsAvailableWithRetry(t *testing.T) {
	t.Parallel()
	d := digest.FromBytes([]byte("layer"))
	store := &fakeInfoProvider{
		calls: map[digest.Digest]int{},
		ready: map[digest.Digest]bool{},
	}
	descs := []ocispecs.Descriptor{{Digest: d, Size: 10}}

	require.False(t, descriptorsAvailableWithRetry(context.Background(), store, descs, 3, time.Millisecond))
	require.Equal(t, 3, store.calls[d])

	store.ready[d] = true
	require.True(t, descriptorsAvailableWithRetry(context.Background(), store, descs, 1, time.Millisecond))
}

type mapLookup map[string]solver.CacheResult

func (m mapLookup) GetResultByID(id string) (solver.CacheResult, error) {
	cr, ok := m[id]
	if !ok {
		return solver.CacheResult{}, solver.ErrNotFound
	}
	return cr, nil
}

func TestResolveCacheResult(t *testing.T) {
	t.Parallel()
	s := &sharedCacheResultStorage{lookup: mapLookup{
		"remote::abc": {ID: "remote::abc", Descriptors: []ocispecs.Descriptor{{Digest: digest.FromBytes([]byte("x")), Size: 1}}},
	}}

	cr, err := s.resolveCacheResult(solver.CacheResult{ID: "remote::abc"})
	require.NoError(t, err)
	require.Len(t, cr.Descriptors, 1)

	cr, err = s.resolveCacheResult(solver.CacheResult{
		ID:          "remote::abc",
		Descriptors: []ocispecs.Descriptor{{Digest: digest.FromBytes([]byte("inline")), Size: 2}},
	})
	require.NoError(t, err)
	require.Equal(t, digest.FromBytes([]byte("inline")), cr.Descriptors[0].Digest)

	_, err = s.resolveCacheResult(solver.CacheResult{ID: "missing::id"})
	require.Error(t, err)
}

func TestDefaultSharedCacheOptions(t *testing.T) {
	t.Parallel()
	opts := DefaultSharedCacheOptions()
	require.False(t, opts.SyncUploadOnSave)
	require.True(t, opts.UploadTopLayerOnly)
	require.True(t, opts.PrefetchOnLoad)
	require.Equal(t, 5, opts.ExistsRetryAttempts)
}
