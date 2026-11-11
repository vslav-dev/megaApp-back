package s3platform

import (
	"context"
	"fmt"
	"os"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awss3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type Config struct {
	Region          string
	Bucket          string
	Endpoint        string
	ForcePathStyle  bool
	StorageClass    string
	AccessKeyID     string
	SecretAccessKey string
}

type Client struct {
	bucket string
	inner  *awss3.Client
}

func NewClient(cfg Config) *Client {
	options := awss3.Options{
		Region:       cfg.Region,
		UsePathStyle: cfg.ForcePathStyle,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
	}
	if cfg.Endpoint != "" {
		options.BaseEndpoint = awssdk.String(cfg.Endpoint)
	}
	return &Client{
		bucket: cfg.Bucket,
		inner:  awss3.New(options),
	}
}

func (c *Client) UploadFile(ctx context.Context, key string, filePath string, contentType string, storageClass string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open archive file: %w", err)
	}
	defer func() { _ = file.Close() }()

	input := &awss3.PutObjectInput{
		Bucket:      awssdk.String(c.bucket),
		Key:         awssdk.String(key),
		Body:        file,
		ContentType: awssdk.String(contentType),
	}
	if storageClass != "" {
		input.StorageClass = awss3types.StorageClass(storageClass)
	}
	if _, err := c.inner.PutObject(ctx, input); err != nil {
		return fmt.Errorf("put object: %w", err)
	}
	return nil
}
