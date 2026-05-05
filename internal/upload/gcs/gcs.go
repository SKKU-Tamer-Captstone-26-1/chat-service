package gcs

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/ontheblock/chat-service/internal/id"
	"github.com/ontheblock/chat-service/internal/upload"
)

const uploadExpiry = 15 * time.Minute
const defaultReadExpiry = 30 * time.Minute

type Signer struct {
	bucket         string
	googleAccessID string
	client         *storage.Client
	handle         *storage.BucketHandle
	readExpiry     time.Duration
}

var safePathSegmentPattern = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

type Option func(*Signer)

func WithReadURLExpiry(expiry time.Duration) Option {
	return func(s *Signer) {
		if expiry > 0 {
			s.readExpiry = expiry
		}
	}
}

func NewSigner(ctx context.Context, bucketName, googleAccessID string, opts ...Option) (*Signer, error) {
	if strings.TrimSpace(bucketName) == "" {
		return nil, fmt.Errorf("bucket name is required")
	}
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	signer := &Signer{
		bucket:         strings.TrimSpace(bucketName),
		googleAccessID: strings.TrimSpace(googleAccessID),
		client:         client,
		handle:         client.Bucket(strings.TrimSpace(bucketName)),
		readExpiry:     defaultReadExpiry,
	}
	for _, opt := range opts {
		opt(signer)
	}
	return signer, nil
}

func (s *Signer) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}

func (s *Signer) CreateAttachmentUploadURL(_ context.Context, roomID, userID, fileName, contentType string) (upload.AttachmentUpload, error) {
	now := time.Now().UTC()
	objectName := buildObjectName(roomID, fileName)
	fileURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s", s.bucket, objectName)
	normalizedContentType := strings.ToLower(strings.TrimSpace(contentType))

	opts := &storage.SignedURLOptions{
		Scheme:      storage.SigningSchemeV4,
		Method:      "PUT",
		Expires:     now.Add(uploadExpiry),
		ContentType: normalizedContentType,
	}
	if s.googleAccessID != "" {
		opts.GoogleAccessID = s.googleAccessID
	}
	uploadURL, err := s.handle.SignedURL(objectName, opts)
	if err != nil {
		return upload.AttachmentUpload{}, err
	}

	return upload.AttachmentUpload{
		ObjectName: objectName,
		UploadURL:  uploadURL,
		FileURL:    fileURL,
		ExpiresAt:  now.Add(uploadExpiry),
	}, nil
}

func (s *Signer) CreateImageUploadURL(ctx context.Context, roomID, userID, fileName, contentType string) (upload.ImageUpload, error) {
	out, err := s.CreateAttachmentUploadURL(ctx, roomID, userID, fileName, contentType)
	if err != nil {
		return upload.ImageUpload{}, err
	}
	return upload.ImageUpload{
		ObjectName: out.ObjectName,
		UploadURL:  out.UploadURL,
		ImageURL:   out.FileURL,
		ExpiresAt:  out.ExpiresAt,
	}, nil
}

func (s *Signer) CreateAttachmentReadURL(_ context.Context, objectName string) (upload.AttachmentRead, error) {
	now := time.Now().UTC()
	trimmedObjectName := strings.TrimSpace(objectName)
	if trimmedObjectName == "" {
		return upload.AttachmentRead{}, fmt.Errorf("object name is required")
	}

	opts := &storage.SignedURLOptions{
		Scheme:  storage.SigningSchemeV4,
		Method:  "GET",
		Expires: now.Add(s.readExpiry),
	}
	if s.googleAccessID != "" {
		opts.GoogleAccessID = s.googleAccessID
	}
	readURL, err := s.handle.SignedURL(trimmedObjectName, opts)
	if err != nil {
		return upload.AttachmentRead{}, err
	}

	return upload.AttachmentRead{
		ObjectName: trimmedObjectName,
		ReadURL:    readURL,
		ExpiresAt:  now.Add(s.readExpiry),
	}, nil
}

func buildObjectName(roomID, fileName string) string {
	base := filepath.Base(strings.TrimSpace(fileName))
	ext := strings.ToLower(filepath.Ext(base))
	if ext == "" {
		ext = ".bin"
	}
	return fmt.Sprintf("chat-attachments/%s/%s%s", sanitizePathSegment(roomID), id.New(), ext)
}

func sanitizePathSegment(value string) string {
	trimmed := strings.TrimSpace(value)
	cleaned := safePathSegmentPattern.ReplaceAllString(trimmed, "-")
	cleaned = strings.Trim(cleaned, "-")
	if cleaned == "" {
		return "unknown"
	}
	return cleaned
}
