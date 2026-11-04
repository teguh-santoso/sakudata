package tenant

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Config holds S3-compatible object storage settings.
type S3Config struct {
	Enabled        bool
	Endpoint       string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	Prefix         string
	ForcePathStyle bool
}

func (c S3Config) key(db, col, id, stored string) (string, error) {
	for _, p := range []string{db, col, id, stored} {
		if p == "" || strings.ContainsAny(p, "/\\") {
			return "", fmt.Errorf("invalid path segment")
		}
	}
	if _, err := SanitizeFileName(stored); err != nil {
		return "", err
	}
	key := strings.Join([]string{db, col, id, stored}, "/")
	if c.Prefix != "" {
		key = strings.Trim(c.Prefix, "/") + "/" + key
	}
	return key, nil
}

// S3Store keeps files in an S3-compatible bucket.
type S3Store struct {
	client *s3.Client
	bucket string
	cfg    S3Config
}

func NewS3Store(ctx context.Context, c S3Config) (*S3Store, error) {
	if c.Bucket == "" || c.AccessKey == "" || c.SecretKey == "" {
		return nil, fmt.Errorf("s3 bucket and credentials are required")
	}
	region := c.Region
	if region == "" {
		region = "auto"
	}
	client := s3.New(s3.Options{
		Region:       region,
		Credentials:  credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, ""),
		UsePathStyle: c.ForcePathStyle,
	})
	if c.Endpoint != "" {
		endpoint := c.Endpoint
		client = s3.New(s3.Options{
			Region:       region,
			Credentials:  credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, ""),
			UsePathStyle: true,
			BaseEndpoint: &endpoint,
		})
	}
	return &S3Store{client: client, bucket: c.Bucket, cfg: c}, nil
}

func (s *S3Store) Put(db, col, id, field, name string, r io.Reader) (string, int64, error) {
	safe, err := SanitizeFileName(name)
	if err != nil {
		return "", 0, err
	}
	stored := StoredName(safe)
	key, err := s.cfg.key(db, col, id, stored)
	if err != nil {
		return "", 0, err
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxUploadSize+1))
	if err != nil {
		return "", 0, err
	}
	if int64(len(data)) > MaxUploadSize {
		return "", 0, fmt.Errorf("file exceeds %d bytes", MaxUploadSize)
	}
	_, err = s.client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
	})
	if err != nil {
		return "", 0, err
	}
	return stored, int64(len(data)), nil
}

func (s *S3Store) Get(db, col, id, field, stored string) (io.ReadCloser, int64, error) {
	key, err := s.cfg.key(db, col, id, stored)
	if err != nil {
		return nil, 0, err
	}
	out, err := s.client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, 0, err
	}
	return out.Body, aws.ToInt64(out.ContentLength), nil
}

func (s *S3Store) Delete(db, col, id, field, stored string) error {
	if stored == "" {
		return nil
	}
	key, err := s.cfg.key(db, col, id, stored)
	if err != nil {
		return err
	}
	_, err = s.client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err
}

// --- generic object helpers (backups, exports) ---

type ObjectInfo struct {
	Key      string `json:"key"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

func (s *S3Store) PutObject(key string, r io.Reader, size int64, contentType string) error {
	in := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          r,
		ContentLength: aws.Int64(size),
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	_, err := s.client.PutObject(context.Background(), in)
	return err
}

func (s *S3Store) GetObject(key string) (io.ReadCloser, int64, error) {
	out, err := s.client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, 0, err
	}
	return out.Body, aws.ToInt64(out.ContentLength), nil
}

func (s *S3Store) ListObjects(prefix string) ([]ObjectInfo, error) {
	var out []ObjectInfo
	var cont *string
	for {
		res, err := s.client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: cont,
		})
		if err != nil {
			return nil, err
		}
		for _, o := range res.Contents {
			out = append(out, ObjectInfo{
				Key:      aws.ToString(o.Key),
				Size:     aws.ToInt64(o.Size),
				Modified: o.LastModified.UTC().Format(time.RFC3339),
			})
		}
		if aws.ToBool(res.IsTruncated) {
			cont = res.NextContinuationToken
			continue
		}
		return out, nil
	}
}

func (s *S3Store) DeleteObject(key string) error {
	_, err := s.client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	return err
}

// DropDB removes every object stored for a database, paging through listings.
func (s *S3Store) DropDB(db string) error {
	if db == "" || strings.ContainsAny(db, "/\\") {
		return fmt.Errorf("invalid database name")
	}
	prefix := db + "/"
	if s.cfg.Prefix != "" {
		prefix = strings.Trim(s.cfg.Prefix, "/") + "/" + prefix
	}
	return s.deletePrefix(prefix)
}

// DropPrefix removes every object stored for one collection.
func (s *S3Store) DropPrefix(db, col string) error {
	if !ValidName(db) || !ValidName(col) {
		return fmt.Errorf("invalid database or collection name")
	}
	prefix := db + "/" + col + "/"
	if s.cfg.Prefix != "" {
		prefix = strings.Trim(s.cfg.Prefix, "/") + "/" + prefix
	}
	return s.deletePrefix(prefix)
}

func (s *S3Store) deletePrefix(prefix string) error {
	var cont *string
	for {
		out, err := s.client.ListObjectsV2(context.Background(), &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: cont,
		})
		if err != nil {
			return err
		}
		if len(out.Contents) > 0 {
			ids := make([]types.ObjectIdentifier, len(out.Contents))
			for i, o := range out.Contents {
				ids[i] = types.ObjectIdentifier{Key: o.Key}
			}
			if _, err := s.client.DeleteObjects(context.Background(), &s3.DeleteObjectsInput{
				Bucket: aws.String(s.bucket),
				Delete: &types.Delete{Objects: ids},
			}); err != nil {
				return err
			}
		}
		if aws.ToBool(out.IsTruncated) {
			cont = out.NextContinuationToken
		} else {
			return nil
		}
	}
}
