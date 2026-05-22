package config

import (
	"os"

	s3remotecache "github.com/moby/buildkit/cache/remotecache/s3"
	"github.com/pkg/errors"
)

// ValidateCache checks cache-related configuration.
func (cfg *Config) ValidateCache() error {
	c := cfg.Cache
	switch c.Backend {
	case "", "bbolt":
		if c.PostgresDSN != "" {
			return errors.New("postgresDSN is only valid when cache.backend=postgres")
		}
	case "postgres":
		if c.PostgresDSN == "" {
			return errors.New("postgresDSN is required when cache.backend=postgres")
		}
	default:
		return errors.Errorf("unsupported cache.backend %q (use bbolt or postgres)", c.Backend)
	}
	if c.S3 != nil {
		if _, err := c.S3.ToS3Config(); err != nil {
			return err
		}
	}
	return nil
}

// ToS3Config converts daemon S3 content store config to remotecache/s3 Config.
func (c *S3ContentStoreConfig) ToS3Config() (s3remotecache.Config, error) {
	if c == nil {
		return s3remotecache.Config{}, errors.New("s3 config is nil")
	}
	bucket := c.Bucket
	if bucket == "" {
		bucket, _ = os.LookupEnv("AWS_BUCKET")
	}
	if bucket == "" {
		return s3remotecache.Config{}, errors.New("cache.s3.bucket (or AWS_BUCKET env) is required")
	}
	region := c.Region
	if region == "" {
		region, _ = os.LookupEnv("AWS_REGION")
	}
	if region == "" {
		return s3remotecache.Config{}, errors.New("cache.s3.region (or AWS_REGION env) is required")
	}
	accessKeyID := c.AccessKeyID
	if accessKeyID == "" {
		accessKeyID = os.Getenv("AWS_ACCESS_KEY_ID")
	}
	secretAccessKey := c.SecretAccessKey
	if secretAccessKey == "" {
		secretAccessKey = os.Getenv("AWS_SECRET_ACCESS_KEY")
	}
	sessionToken := c.SessionToken
	if sessionToken == "" {
		sessionToken = os.Getenv("AWS_SESSION_TOKEN")
	}
	blobsPrefix := c.BlobsPrefix
	if blobsPrefix == "" {
		blobsPrefix = "blobs/"
	}
	return s3remotecache.Config{
		Bucket:          bucket,
		Region:          region,
		Prefix:          c.Prefix,
		BlobsPrefix:     blobsPrefix,
		EndpointURL:     c.EndpointURL,
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		SessionToken:      sessionToken,
		UsePathStyle:    c.UsePathStyle,
		UploadParallelism: 4,
	}, nil
}
