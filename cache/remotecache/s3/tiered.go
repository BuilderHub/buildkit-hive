package s3

import (
	"context"
	"io"
	"sync"

	"github.com/containerd/containerd/v2/core/content"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/locker"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// TieredStore is a content.Store that uses local SSD as hot cache and S3 as shared backing store.
type TieredStore struct {
	local     content.Store
	s3        *s3Client
	pullLocks *locker.Locker
	uploadWG  sync.WaitGroup
}

var _ content.Store = (*TieredStore)(nil)

// NewTieredContentStore creates a tiered content store backed by local storage and S3.
func NewTieredContentStore(ctx context.Context, local content.Store, cfg Config) (content.Store, error) {
	s3c, err := newS3Client(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &TieredStore{
		local:     local,
		s3:        s3c,
		pullLocks: locker.New(),
	}, nil
}

func (t *TieredStore) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	info, err := t.local.Info(ctx, dgst)
	if err == nil {
		return info, nil
	}
	if !cerrdefs.IsNotFound(err) {
		return content.Info{}, err
	}
	key := t.s3.blobKey(dgst)
	if lastMod, _, err := t.s3.exists(ctx, key); err != nil {
		return content.Info{}, err
	} else if lastMod != nil {
		return content.Info{Digest: dgst}, nil
	}
	return content.Info{}, err
}

func (t *TieredStore) Update(ctx context.Context, info content.Info, fieldpaths ...string) (content.Info, error) {
	return t.local.Update(ctx, info, fieldpaths...)
}

func (t *TieredStore) Walk(ctx context.Context, fn content.WalkFunc, filters ...string) error {
	return t.local.Walk(ctx, fn, filters...)
}

func (t *TieredStore) Delete(ctx context.Context, dgst digest.Digest) error {
	return t.local.Delete(ctx, dgst)
}

func (t *TieredStore) Status(ctx context.Context, ref string) (content.Status, error) {
	return t.local.Status(ctx, ref)
}

func (t *TieredStore) ListStatuses(ctx context.Context, filters ...string) ([]content.Status, error) {
	return t.local.ListStatuses(ctx, filters...)
}

func (t *TieredStore) Abort(ctx context.Context, ref string) error {
	return t.local.Abort(ctx, ref)
}

func (t *TieredStore) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	ra, err := t.local.ReaderAt(ctx, desc)
	if err == nil {
		return ra, nil
	}
	if !cerrdefs.IsNotFound(err) {
		return nil, err
	}

	t.pullLocks.Lock(desc.Digest.String())
	defer t.pullLocks.Unlock(desc.Digest.String())

	if ra, err = t.local.ReaderAt(ctx, desc); err == nil {
		return ra, nil
	} else if !cerrdefs.IsNotFound(err) {
		return nil, err
	}

	if err := t.pullFromS3(ctx, desc); err != nil {
		return nil, errors.Wrapf(err, "failed to pull %s from S3", desc.Digest)
	}
	return t.local.ReaderAt(ctx, desc)
}

func (t *TieredStore) pullFromS3(ctx context.Context, desc ocispecs.Descriptor) error {
	key := t.s3.blobKey(desc.Digest)

	lastMod, size, err := t.s3.exists(ctx, key)
	if err != nil {
		return err
	}
	if lastMod == nil {
		return errors.Errorf("blob %s not found in S3", desc.Digest)
	}
	if size != nil && *size != desc.Size && desc.Size > 0 {
		bklog.G(ctx).Warnf("S3 blob size mismatch for %s: expected %d got %d", desc.Digest, desc.Size, *size)
	}

	ra, err := t.s3.ReaderAt(ctx, desc)
	if err != nil {
		return errors.Wrap(err, "failed to get S3 reader")
	}
	defer ra.Close()

	w, err := t.local.Writer(ctx, content.WithRef("s3-pull-"+desc.Digest.String()), content.WithDescriptor(desc))
	if err != nil {
		return errors.Wrap(err, "failed to create local writer for S3 pull")
	}

	_, err = io.Copy(w, io.NewSectionReader(ra, 0, ra.Size()))
	if err != nil {
		w.Close()
		return errors.Wrap(err, "failed to copy S3 content to local")
	}

	if err := w.Commit(ctx, desc.Size, desc.Digest); err != nil {
		w.Close()
		if cerrdefs.IsAlreadyExists(err) {
			return nil
		}
		return errors.Wrap(err, "failed to commit pulled content locally")
	}
	return w.Close()
}

func (t *TieredStore) Writer(ctx context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	w, err := t.local.Writer(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &tieredWriter{
		Writer: w,
		store:  t,
		ctx:    ctx,
	}, nil
}

type tieredWriter struct {
	content.Writer
	store *TieredStore
	ctx   context.Context
}

func (tw *tieredWriter) Commit(ctx context.Context, size int64, expected digest.Digest, opts ...content.Opt) error {
	if err := tw.Writer.Commit(ctx, size, expected, opts...); err != nil {
		return err
	}
	tw.store.uploadAsync(ctx, expected, size)
	return nil
}

func (t *TieredStore) uploadAsync(ctx context.Context, dgst digest.Digest, size int64) {
	t.uploadWG.Add(1)
	go func() {
		defer t.uploadWG.Done()
		uploadCtx := context.WithoutCancel(ctx)
		key := t.s3.blobKey(dgst)
		if lastMod, _, err := t.s3.exists(uploadCtx, key); err != nil {
			bklog.G(uploadCtx).WithError(err).Warnf("failed to check S3 for blob %s", dgst)
			return
		} else if lastMod != nil {
			return
		}

		ra, err := t.local.ReaderAt(uploadCtx, ocispecs.Descriptor{Digest: dgst, Size: size})
		if err != nil {
			bklog.G(uploadCtx).WithError(err).Warnf("failed to read local blob %s for S3 upload", dgst)
			return
		}
		defer ra.Close()

		if err := t.s3.saveMutableAt(uploadCtx, key, io.NewSectionReader(ra, 0, ra.Size())); err != nil {
			bklog.G(uploadCtx).WithError(err).Warnf("failed to upload blob %s to S3", dgst)
		}
	}()
}
