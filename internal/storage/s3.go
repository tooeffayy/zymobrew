package storage

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type s3Store struct {
	client *minio.Client
	bucket string
}

func newS3(bc BackendConfig) (*s3Store, error) {
	if bc.S3Bucket == "" {
		return nil, fmt.Errorf("storage: S3 bucket is required when backend=s3")
	}
	endpoint := bc.S3Endpoint
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}
	// Strip the scheme if present — minio-go expects host[:port] only.
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		endpoint = u.Host
	}
	useSSL := true
	if u, err := url.Parse(bc.S3Endpoint); err == nil && u.Scheme == "http" {
		useSSL = false
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(bc.S3AccessKey, bc.S3SecretKey, ""),
		Secure: useSSL,
		Region: bc.S3Region,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: s3 client: %w", err)
	}
	return &s3Store{client: client, bucket: bc.S3Bucket}, nil
}

func (s *s3Store) Backend() string { return "s3" }

func (s *s3Store) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("storage: s3 put %q: %w", key, err)
	}
	return nil
}

func (s *s3Store) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, fmt.Errorf("storage: s3 get %q: %w", key, err)
	}
	info, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return nil, 0, fmt.Errorf("storage: s3 stat %q: %w", key, err)
	}
	return obj, info.Size, nil
}

func (s *s3Store) Delete(ctx context.Context, key string) error {
	err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		// Treat not-found as no-op.
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil
		}
		return fmt.Errorf("storage: s3 delete %q: %w", key, err)
	}
	return nil
}

func (s *s3Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, ttl, url.Values{})
	if err != nil {
		return "", fmt.Errorf("storage: s3 presign %q: %w", key, err)
	}
	return u.String(), nil
}
