package storage

import (
	"context"
	"fmt"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"io"
	"time"
)

type ObjectStore interface {
	Put(context.Context, string, io.Reader, int64, string) error
	Get(context.Context, string) (io.ReadCloser, error)
	Delete(context.Context, string) error
	PresignGet(context.Context, string, time.Duration) (string, error)
	Health(context.Context) error
}
type Store struct {
	client *minio.Client
	bucket string
}

func New(endpoint, bucket, access, secret string, ssl bool) (*Store, error) {
	c, e := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(access, secret, ""), Secure: ssl})
	if e != nil {
		return nil, fmt.Errorf("invalid object storage configuration")
	}
	return &Store{c, bucket}, nil
}
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, e := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	return e
}
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
}
func (s *Store) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}
func (s *Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	u, e := s.client.PresignedGetObject(ctx, s.bucket, key, ttl, nil)
	if e != nil {
		return "", e
	}
	return u.String(), nil
}
func (s *Store) Health(ctx context.Context) error {
	ok, e := s.client.BucketExists(ctx, s.bucket)
	if e != nil {
		return e
	}
	if !ok {
		return fmt.Errorf("bucket unavailable")
	}
	return nil
}
