package repository

import (
	"context"
	"time"

	"github.com/ontheblock/chat-service/internal/domain"
)

type TxRunner interface {
	WithTx(ctx context.Context, fn func(ctx context.Context, repo ChatRepository) error) error
}

type ChatRepository interface {
	CreateRoom(ctx context.Context, room domain.ChatRoom) error
	GetRoom(ctx context.Context, roomID string) (domain.ChatRoom, error)
	GetActiveBoardLinkedRoom(ctx context.Context, boardID string) (domain.ChatRoom, error)
	UpdateRoom(ctx context.Context, room domain.ChatRoom) error
	ListRoomsByUser(ctx context.Context, userID string, limit int, pageToken string) ([]domain.ChatRoomSummary, string, error)

	GetMember(ctx context.Context, roomID, userID string) (domain.ChatRoomMember, error)
	CreateMember(ctx context.Context, member domain.ChatRoomMember) error
	UpdateMember(ctx context.Context, member domain.ChatRoomMember) error
	ListActiveMembersByJoinOrder(ctx context.Context, roomID string) ([]domain.ChatRoomMember, error)

	CreateMessageWithNextSequence(ctx context.Context, msg domain.ChatMessage) (domain.ChatMessage, error)
	GetMessage(ctx context.Context, roomID, messageID string) (domain.ChatMessage, error)
	UpdateMessage(ctx context.Context, msg domain.ChatMessage) error
	ListMessagesBefore(ctx context.Context, roomID string, beforeSequence int64, limit int) ([]domain.ChatMessage, int64, error)
	ListMessagesAfter(ctx context.Context, roomID string, afterSequence int64, limit int) ([]domain.ChatMessage, error)

	UpsertDeviceToken(ctx context.Context, token domain.DeviceToken) (domain.DeviceToken, error)
	DeactivateDeviceToken(ctx context.Context, userID, deviceID string, now time.Time) error
	ListActiveDeviceTokensByUserIDs(ctx context.Context, userIDs []string) ([]domain.DeviceToken, error)
}
