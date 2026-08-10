package storage

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	appconfig "github.com/okoye-dev/lapis-archive-file-service/internal/config"
)

const PresignTTL = time.Hour

// Storage is the object store. File BYTES never pass through this service:
// clients upload and download directly via presigned URLs. The methods here
// are the control plane only — presign issuance, a HEAD to confirm a file
// exists, and listing/deletion.
type Storage interface {
	GetPresignedUploadURL(ctx context.Context, key string, size int64, contentType string) (string, error)
	GetPresignedURL(ctx context.Context, key string, forceDownload bool) (string, error)
	DeleteFile(ctx context.Context, key string) error
	ListFiles(ctx context.Context) ([]string, error)
	GetFileSize(ctx context.Context, key string) (int64, error)
}

type S3Storage struct {
	client     *s3.Client
	bucketName string
}

func NewS3Storage(cfg *appconfig.S3Config) (*S3Storage, error) {
	httpClient := &http.Client{
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		),
		config.WithHTTPClient(httpClient),
	)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.ForcePathStyle
		if cfg.Endpoint != "" {
			scheme := "https://"
			if !cfg.UseSSL {
				scheme = "http://"
			}
			o.BaseEndpoint = aws.String(scheme + cfg.Endpoint)
		}
	})

	return &S3Storage{
		client:     client,
		bucketName: cfg.BucketName,
	}, nil
}

func (s *S3Storage) DeleteFile(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("deleting %q: %w", key, err)
	}

	return nil
}

func (s *S3Storage) ListFiles(ctx context.Context) ([]string, error) {
	var keys []string

	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucketName),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing files: %w", err)
		}
		for _, obj := range page.Contents {
			keys = append(keys, aws.ToString(obj.Key))
		}
	}

	return keys, nil
}

func (s *S3Storage) GetFileSize(ctx context.Context, key string) (int64, error) {
	result, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return 0, fmt.Errorf("sizing %q: %w", key, err)
	}

	return aws.ToInt64(result.ContentLength), nil
}

const UploadPresignTTL = 15 * time.Minute

func (s *S3Storage) GetPresignedUploadURL(ctx context.Context, key string, size int64, contentType string) (string, error) {
	input := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucketName),
		Key:           aws.String(key),
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	}

	request, err := s3.NewPresignClient(s.client).PresignPutObject(ctx, input, func(opts *s3.PresignOptions) {
		opts.Expires = UploadPresignTTL
	})
	if err != nil {
		return "", fmt.Errorf("presigning upload %q: %w", key, err)
	}

	return request.URL, nil
}

func (s *S3Storage) GetPresignedURL(ctx context.Context, key string, forceDownload bool) (string, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	}

	if forceDownload {
		original := key
		if _, name, found := strings.Cut(key, "_"); found {
			original = name
		}
		input.ResponseContentDisposition = aws.String(fmt.Sprintf("attachment; filename=%q", original))
	}

	request, err := s3.NewPresignClient(s.client).PresignGetObject(ctx, input, func(opts *s3.PresignOptions) {
		opts.Expires = PresignTTL
	})
	if err != nil {
		return "", fmt.Errorf("presigning %q: %w", key, err)
	}

	return request.URL, nil
}
