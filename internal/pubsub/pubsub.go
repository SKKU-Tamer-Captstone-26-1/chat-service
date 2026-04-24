package pubsub

import (
	"context"

	"github.com/ontheblock/chat-service/internal/domain"
)

type Subscription interface {
	C() <-chan domain.ChatMessage
	Close()
}

type RoomPubSub interface {
	Publish(ctx context.Context, roomID string, msg domain.ChatMessage)
	Subscribe(ctx context.Context, roomID string, buffer int) Subscription
}
