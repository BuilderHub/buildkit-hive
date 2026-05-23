package s3

import (
	"testing"

	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestBlobKeyGrouped(t *testing.T) {
	t.Parallel()
	c := &s3Client{group: "global", manifestsPrefix: "manifests/"}
	dgst := digest.FromString("abc")
	require.Equal(t, "global/buildkitblobs/"+dgst.String(), c.blobKey(dgst))

	c.group = "team-x"
	require.Equal(t, "team-x/buildkitblobs/"+dgst.String(), c.blobKey(dgst))
}

func TestManifestKeyGrouped(t *testing.T) {
	t.Parallel()
	c := &s3Client{group: "team-a", manifestsPrefix: "manifests/"}
	require.Equal(t, "team-a/manifests/buildkit", c.manifestKey("buildkit"))
}

func TestValidateCacheGroup(t *testing.T) {
	t.Parallel()
	g, err := ValidateCacheGroup("")
	require.NoError(t, err)
	require.Equal(t, DefaultCacheGroup, g)

	g, err = ValidateCacheGroup("team_1")
	require.NoError(t, err)
	require.Equal(t, "team_1", g)

	_, err = ValidateCacheGroup("bad/group")
	require.Error(t, err)
}
