package s3

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Service interface {
	UploadToS3(ctx context.Context, file io.Reader, key string, contentType string) (string, error)
	GenerateViewUrl(ctx context.Context, key string, expiresIn time.Duration) (string, error)
	GetObject(ctx context.Context, key string) ([]byte, error)
	GetObjectLimited(ctx context.Context, key string, maxBytes int64) ([]byte, error)
}

type cachedURL struct {
	url       string
	expiresAt time.Time
}

type s3ServiceImpl struct {
	client        *s3.Client
	presignClient *s3.PresignClient
	bucketName    string
	cdnBaseURL    string
	urlCache      sync.Map // key string -> cachedURL
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

	cdnURL := strings.TrimRight(strings.TrimSpace(os.Getenv("CDN_BASE_URL")), "/")
	if cdnURL == "" {
		cdnURL = strings.TrimRight(strings.TrimSpace(os.Getenv("CLOUDFRONT_URL")), "/")
	}

	client := s3.NewFromConfig(cfg)
	presignClient := s3.NewPresignClient(client)

	return &s3ServiceImpl{
		client:        client,
		presignClient: presignClient,
		bucketName:    bucket,
		cdnBaseURL:    cdnURL,
	}, nil
}

func (s *s3ServiceImpl) UploadToS3(ctx context.Context, file io.Reader, key string, contentType string) (string, error) {
	input := &s3.PutObjectInput{
		Bucket:      aws.String(s.bucketName),
		Key:         aws.String(key),
		Body:        file,
		ContentType: aws.String(contentType),
	}

	// Asignar Cache-Control inmutable (1 año) para imágenes y assets estáticos públicos
	if strings.HasPrefix(contentType, "image/") ||
		strings.HasPrefix(key, "events/") ||
		strings.HasPrefix(key, "posts/") ||
		strings.HasPrefix(key, "avatars/") {
		input.CacheControl = aws.String("public, max-age=31536000, immutable")
	}

	_, err := s.client.PutObject(ctx, input)
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

	// 1. Si hay un CDN / CloudFront configurado, devolver URL estática permanente sin firmas
	if s.cdnBaseURL != "" {
		cleanKey := strings.TrimLeft(key, "/")
		return fmt.Sprintf("%s/%s", s.cdnBaseURL, cleanKey), nil
	}

	// 2. Comprobar si ya tenemos la URL firmada en caché en memoria y aún es válida
	// Reutilizar mientras quede al menos el 20% del tiempo de expiración
	minRemaining := expiresIn / 5
	if minRemaining < 10*time.Minute {
		minRemaining = 10 * time.Minute
	}
	if val, ok := s.urlCache.Load(key); ok {
		if item, ok := val.(cachedURL); ok {
			if time.Now().Add(minRemaining).Before(item.expiresAt) {
				return item.url, nil
			}
		}
	}

	// 3. Generar nueva URL firmada en S3
	request, err := s.presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expiresIn))

	if err != nil {
		log.Printf("Error generando URL firmada para %s: %v\n", key, err)
		return "", fmt.Errorf("couldn't get presigned URL: %w", err)
	}

	// Guardar en caché en memoria para que la URL sea constante entre peticiones
	s.urlCache.Store(key, cachedURL{
		url:       request.URL,
		expiresAt: time.Now().Add(expiresIn),
	})

	return request.URL, nil
}

func (s *s3ServiceImpl) GetObject(ctx context.Context, key string) ([]byte, error) {
	return s.GetObjectLimited(ctx, key, 0)
}

// Bound public artwork reads without changing existing consumers' semantics.
func (s *s3ServiceImpl) GetObjectLimited(ctx context.Context, key string, maxBytes int64) ([]byte, error) {
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

	var reader io.Reader = result.Body
	if maxBytes > 0 {
		reader = io.LimitReader(result.Body, maxBytes+1)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read object body: %w", err)
	}

	return body, nil
}
