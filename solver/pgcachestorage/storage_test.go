package pgcachestorage

import (
	"context"
	"os"
	"testing"

	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/testutil"
	"github.com/stretchr/testify/require"
)

func TestPostgresCacheStorage(t *testing.T) {
	dsn := os.Getenv("BUILDKIT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BUILDKIT_TEST_POSTGRES_DSN not set")
	}

	testutil.RunCacheStorageTests(t, func() solver.CacheKeyStorage {
		st, err := NewStore(context.TODO(), dsn)
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, st.Close())
		})
		return st
	})
}
