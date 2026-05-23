package pgcachestorage

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/moby/buildkit/solver"
	"github.com/stretchr/testify/require"
)

func TestPostgresCacheGroupIsolation(t *testing.T) {
	dsn := os.Getenv("BUILDKIT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BUILDKIT_TEST_POSTGRES_DSN not set")
	}

	ctx := context.Background()
	a, err := NewStore(ctx, dsn, "team-a")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, a.Close()) })

	b, err := NewStore(ctx, dsn, "team-b")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, b.Close()) })

	const cacheKeyID = "test-cache-key-id"
	const resultID = "worker::ref"

	res := solver.CacheResult{
		ID:        resultID,
		CreatedAt: time.Now(),
	}
	require.NoError(t, a.AddResult(cacheKeyID, res))
	require.True(t, a.Exists(cacheKeyID))
	require.False(t, b.Exists(cacheKeyID))

	_, err = b.Load(cacheKeyID, resultID)
	require.Error(t, err)
	require.ErrorIs(t, err, solver.ErrNotFound)
}
