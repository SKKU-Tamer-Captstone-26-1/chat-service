package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ontheblock/chat-service/internal/pubsub"
	"github.com/ontheblock/chat-service/internal/repository/memory"
	"github.com/ontheblock/chat-service/internal/service"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	store := memory.NewStore()
	ps := pubsub.NewMemoryRoomPubSub()
	_ = service.New(store, store, ps)

	log.Println("chat-service scaffold initialized (in-memory mode)")
	<-ctx.Done()
	log.Println("chat-service shutdown")
}
