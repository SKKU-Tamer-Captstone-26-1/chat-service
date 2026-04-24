package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ontheblock/chat-service/internal/pubsub"
	"github.com/ontheblock/chat-service/internal/repository/memory"
	"github.com/ontheblock/chat-service/internal/service"
	transportgrpc "github.com/ontheblock/chat-service/internal/transport/grpc"
	chatv1 "github.com/ontheblock/chat-service/proto/chat/v1"
	"google.golang.org/grpc"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	addr := envOrDefault("CHAT_SERVICE_ADDR", ":9090")
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen failed: %v", err)
	}

	store := memory.NewStore()
	ps := pubsub.NewMemoryRoomPubSub()
	svc := service.New(store, store, ps)
	handler := transportgrpc.NewServer(svc)

	grpcServer := grpc.NewServer()
	chatv1.RegisterChatServiceServer(grpcServer, handler)

	go func() {
		log.Printf("chat-service listening on %s (in-memory repository)", addr)
		if err := grpcServer.Serve(lis); err != nil {
			log.Printf("grpc serve stopped: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	done := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		grpcServer.Stop()
	}
	log.Println("chat-service shutdown complete")
}

func envOrDefault(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}
