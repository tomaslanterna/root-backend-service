package s3

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Service interface {
	UploadToS3(ctx context.Context, file io.Reader, key string, contentType string) (string, error)
	GenerateViewUrl(ctx context.Context, key string, expiresIn time.Duration) (string, error)
	GetObject(ctx context.Context, key string) ([]byte, error)
}

type s3ServiceImpl struct {
	client        *s3.Client
	presignClient *s3.PresignClient
	bucketName    string
}

func NewS3Service(ctx context.Context) (S3Service, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(os.Getenv("AWS_REGION")))
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config, %v", err)
	}

	bucket := os.Getenv("AWS_BUCKET_NAME")
	if bucket == "" {
		bucket = "default-kyc-bucket" // Mock default if not set
	}

	client := s3.NewFromConfig(cfg)
	presignClient := s3.NewPresignClient(client)

	return &s3ServiceImpl{
		client:        client,
		presignClient: presignClient,
		bucketName:    bucket,
	}, nil
}

func (s *s3ServiceImpl) UploadToS3(ctx context.Context, file io.Reader, key string, contentType string) (string, error) {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucketName),
		Key:         aws.String(key),
		Body:        file,
		ContentType: aws.String(contentType),
	})

	if err != nil {
		log.Printf("Fallo al subir a S3 (%s). Error: %v\n", key, err)
		return "", fmt.Errorf("failed to upload file: %w", err)
	}

	return key, nil
}

func (s *s3ServiceImpl) GenerateViewUrl(ctx context.Context, key string, expiresIn time.Duration) (string, error) {
	if key == "" {
		return "", fmt.Errorf("key cannot be empty")
	}

	request, err := s.presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expiresIn))

	if err != nil {
		log.Printf("Error generando URL firmada para %s: %v\n", key, err)
		return "", fmt.Errorf("couldn't get presigned URL: %w", err)
	}

	return request.URL, nil
}

func (s *s3ServiceImpl) GetObject(ctx context.Context, key string) ([]byte, error) {
	if key == "" {
		return nil, fmt.Errorf("key cannot be empty")
	}

	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get object from S3: %w", err)
	}
	defer result.Body.Close()

	body, err := io.ReadAll(result.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read object body: %w", err)
	}

	return body, nil
}
