package s3

import (
	"testing"

	"github.com/containerd/containerd/v2/core/content"
)

func TestTieredStoreImplementsContentStore(t *testing.T) {
	var _ content.Store = (*TieredStore)(nil)
}
