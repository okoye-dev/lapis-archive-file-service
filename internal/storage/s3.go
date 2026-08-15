package storage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	appconfig "github.com/okoye-dev/lapis-archive-file-service/internal/config"
)

const PresignTTL = time.Hour

// Storage is the object store. File bytes never pass through this service:
// clients upload and download directly via presigned URLs. These methods are
// the control plane only (presign, HEAD, delete).
type Storage interface {
	GetPresignedUploadURL(ctx context.Context, key string, size int64, contentType string) (string, error)
	GetPresignedURL(ctx context.Context, key string, forceDownload bool) (string, error)
	DeleteFile(ctx context.Context, key string) error
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

// ErrNoSuchUpload means R2 no longer has this multipart session; restart it.
var ErrNoSuchUpload = errors.New("multipart upload not found")

type Part struct {
	PartNumber int32  `json:"part_number"`
	ETag       string `json:"etag"`
	Size       int64  `json:"size,omitempty"`
}

func (s *S3Storage) CreateMultipartUpload(ctx context.Context, key, contentType string) (string, error) {
	out, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(s.bucketName),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("creating multipart upload %q: %w", key, err)
	}
	return aws.ToString(out.UploadId), nil
}

func (s *S3Storage) PresignUploadPart(ctx context.Context, key, uploadID string, partNumber int32) (string, error) {
	request, err := s3.NewPresignClient(s.client).PresignUploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(s.bucketName),
		Key:        aws.String(key),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(partNumber),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = UploadPresignTTL
	})
	if err != nil {
		return "", fmt.Errorf("presigning part %d of %q: %w", partNumber, key, err)
	}
	return request.URL, nil
}

// ListParts reports what the bucket actually holds, for resuming.
func (s *S3Storage) ListParts(ctx context.Context, key, uploadID string) ([]Part, error) {
	var parts []Part
	var marker *string
	for {
		out, err := s.client.ListParts(ctx, &s3.ListPartsInput{
			Bucket:           aws.String(s.bucketName),
			Key:              aws.String(key),
			UploadId:         aws.String(uploadID),
			PartNumberMarker: marker,
		})
		if err != nil {
			return nil, wrapNoSuchUpload("listing parts of %q", key, err)
		}
		for _, p := range out.Parts {
			parts = append(parts, Part{
				PartNumber: aws.ToInt32(p.PartNumber),
				ETag:       aws.ToString(p.ETag),
				Size:       aws.ToInt64(p.Size),
			})
		}
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		marker = out.NextPartNumberMarker
	}
	return parts, nil
}

func (s *S3Storage) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []Part) error {
	// R2 requires ascending part numbers.
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })

	completed := make([]types.CompletedPart, len(parts))
	for i, p := range parts {
		completed[i] = types.CompletedPart{
			PartNumber: aws.Int32(p.PartNumber),
			ETag:       aws.String(p.ETag),
		}
	}

	_, err := s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(s.bucketName),
		Key:             aws.String(key),
		UploadId:        aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: completed},
	})
	if err != nil {
		return wrapNoSuchUpload("completing multipart upload %q", key, err)
	}
	return nil
}

func (s *S3Storage) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	_, err := s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(s.bucketName),
		Key:      aws.String(key),
		UploadId: aws.String(uploadID),
	})
	if err != nil {
		return wrapNoSuchUpload("aborting multipart upload %q", key, err)
	}
	return nil
}

func wrapNoSuchUpload(format, key string, err error) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchUpload" {
		return ErrNoSuchUpload
	}
	return fmt.Errorf(format+": %w", key, err)
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
