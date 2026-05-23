package s3

import (
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestTieredStoreImplementsContentStore(t *testing.T) {
	var _ content.Store = (*TieredStore)(nil)
}

func TestEnsureUploadedEmptyDescriptors(t *testing.T) {
	t.Parallel()
	ts := &TieredStore{uploadParallelism: 4}
	require.NoError(t, ts.EnsureUploaded(t.Context(), nil, false))
}

func TestUploadDescsTopLayerOnly(t *testing.T) {
	t.Parallel()
	d1 := ocispecs.Descriptor{Digest: digest.FromBytes([]byte("a")), Size: 1}
	d2 := ocispecs.Descriptor{Digest: digest.FromBytes([]byte("b")), Size: 2}
	all := []ocispecs.Descriptor{d1, d2}

	require.Equal(t, all, uploadDescs(all, false))
	require.Equal(t, []ocispecs.Descriptor{d2}, uploadDescs(all, true))
	require.Nil(t, uploadDescs(nil, true))
}
