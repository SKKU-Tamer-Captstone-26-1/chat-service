package upload

import (
	"context"
	"time"
)

type ImageUpload struct {
	ObjectName string
	UploadURL  string
	ImageURL   string
	ExpiresAt  time.Time
}

type AttachmentUpload struct {
	ObjectName string
	UploadURL  string
	FileURL    string
	ExpiresAt  time.Time
}

type AttachmentRead struct {
	ObjectName string
	ReadURL    string
	ExpiresAt  time.Time
}

type AttachmentUploadSigner interface {
	CreateAttachmentUploadURL(ctx context.Context, roomID, userID, fileName, contentType string) (AttachmentUpload, error)
}

type ImageUploadSigner interface {
	CreateImageUploadURL(ctx context.Context, roomID, userID, fileName, contentType string) (ImageUpload, error)
}

type AttachmentReadURLSigner interface {
	CreateAttachmentReadURL(ctx context.Context, objectName string) (AttachmentRead, error)
}
