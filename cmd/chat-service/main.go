package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/ontheblock/chat-service/internal/pubsub"
	"github.com/ontheblock/chat-service/internal/repository"
	"github.com/ontheblock/chat-service/internal/repository/memory"
	postgresrepo "github.com/ontheblock/chat-service/internal/repository/postgres"
	"github.com/ontheblock/chat-service/internal/service"
	transportgrpc "github.com/ontheblock/chat-service/internal/transport/grpc"
	gcsupload "github.com/ontheblock/chat-service/internal/upload/gcs"
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

	store, cleanup, storeLabel, err := initRepository(ctx)
	if err != nil {
		log.Fatalf("repository init failed: %v", err)
	}
	defer cleanup()

	ps := pubsub.NewMemoryRoomPubSub()
	opts := []service.Option{}
	if signer, ok, err := initAttachmentUploadSigner(); err != nil {
		log.Fatalf("attachment upload signer init failed: %v", err)
	} else if ok {
		defer func() {
			if err := signer.Close(); err != nil {
				log.Printf("attachment upload signer close failed: %v", err)
			}
		}()
		opts = append(opts, service.WithAttachmentUploadSigner(signer))
		opts = append(opts, service.WithAttachmentReadSigner(signer))
		opts = append(opts, service.WithTrustedAttachmentBucket(strings.TrimSpace(os.Getenv("GCP_STORAGE_BUCKET"))))
		log.Printf("attachment upload signer enabled for bucket %s", os.Getenv("GCP_STORAGE_BUCKET"))
	}
	svc := service.New(store, store, ps, opts...)
	handler := transportgrpc.NewServer(svc)

	grpcServer := grpc.NewServer()
	chatv1.RegisterChatServiceServer(grpcServer, handler)

	go func() {
		log.Printf("chat-service listening on %s (%s repository)", addr, storeLabel)
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

func initRepository(ctx context.Context) (runtimeRepository, func(), string, error) {
	repoType := envOrDefault("CHAT_REPOSITORY", "postgres")
	switch repoType {
	case "memory":
		store := memory.NewStore()
		return store, func() {}, "memory", nil
	case "postgres":
		dsn := os.Getenv("CHAT_DB_DSN")
		if dsn == "" {
			return nil, nil, "", errors.New("CHAT_DB_DSN is required when CHAT_REPOSITORY=postgres")
		}
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, nil, "", err
		}
		pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := db.PingContext(pingCtx); err != nil {
			_ = db.Close()
			return nil, nil, "", err
		}
		store := postgresrepo.New(db)
		return store, func() { _ = db.Close() }, "postgres", nil
	default:
		return nil, nil, "", errors.New("CHAT_REPOSITORY must be one of: postgres, memory")
	}
}

type runtimeRepository interface {
	repository.TxRunner
	repository.ChatRepository
}

func initAttachmentUploadSigner() (*gcsupload.Signer, bool, error) {
	bucket := strings.TrimSpace(os.Getenv("GCP_STORAGE_BUCKET"))
	googleAccessID := strings.TrimSpace(os.Getenv("GCP_SIGNING_SERVICE_ACCOUNT_EMAIL"))
	if bucket == "" {
		return nil, false, nil
	}
	signer, err := gcsupload.NewSigner(context.Background(), bucket, googleAccessID, gcsupload.WithReadURLExpiry(readURLExpiry()))
	if err != nil {
		return nil, false, err
	}
	return signer, true, nil
}

func readURLExpiry() time.Duration {
	const defaultMinutes = 30
	raw := strings.TrimSpace(os.Getenv("GCS_READ_URL_EXPIRES_MINUTES"))
	if raw == "" {
		return defaultMinutes * time.Minute
	}
	minutes, err := time.ParseDuration(raw + "m")
	if err != nil || minutes <= 0 {
		return defaultMinutes * time.Minute
	}
	return minutes
}
