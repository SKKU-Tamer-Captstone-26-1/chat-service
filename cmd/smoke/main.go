package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	chatv1 "github.com/ontheblock/chat-service/proto/chat/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", envOrDefault("CHAT_SERVICE_SMOKE_ADDR", "localhost:9090"), "chat-service grpc address")
	ownerID := flag.String("owner", "11111111-1111-1111-1111-111111111111", "owner user id (uuid)")
	memberID := flag.String("member", "22222222-2222-2222-2222-222222222222", "member user id (uuid)")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, *addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	client := chatv1.NewChatServiceClient(conn)

	roomResp, err := client.CreateRoom(ctx, &chatv1.CreateRoomRequest{
		CreatorUserId: *ownerID,
		Title:         fmt.Sprintf("smoke-%d", time.Now().Unix()),
	})
	must("CreateRoom", err)
	roomID := roomResp.GetRoom().GetRoomId()
	if roomID == "" {
		log.Fatal("CreateRoom returned empty room_id")
	}

	_, err = client.JoinRoom(ctx, &chatv1.JoinRoomRequest{RoomId: roomID, UserId: *memberID})
	must("JoinRoom", err)

	sendResp, err := client.SendMessage(ctx, &chatv1.SendMessageRequest{
		RoomId:       roomID,
		SenderUserId: *ownerID,
		MessageType:  chatv1.MessageType_MESSAGE_TYPE_TEXT,
		Content:      "smoke-check",
	})
	must("SendMessage", err)
	messageID := sendResp.GetMessage().GetMessageId()
	if messageID == "" {
		log.Fatal("SendMessage returned empty message_id")
	}

	getResp, err := client.GetMessages(ctx, &chatv1.GetMessagesRequest{
		RoomId: roomID,
		UserId: *ownerID,
		Limit:  20,
	})
	must("GetMessages", err)
	if len(getResp.GetMessages()) == 0 {
		log.Fatal("GetMessages returned no messages")
	}
	if getResp.GetMessages()[0].GetMessageId() != messageID {
		log.Fatalf("GetMessages latest message mismatch: expected=%s got=%s", messageID, getResp.GetMessages()[0].GetMessageId())
	}

	_, err = client.MarkAsRead(ctx, &chatv1.MarkAsReadRequest{
		RoomId:             roomID,
		UserId:             *ownerID,
		LastReadSequenceNo: sendResp.GetMessage().GetSequenceNo(),
	})
	must("MarkAsRead", err)

	log.Printf("SMOKE PASS addr=%s room_id=%s message_id=%s", *addr, roomID, messageID)
}

func must(step string, err error) {
	if err != nil {
		log.Fatalf("%s failed: %v", step, err)
	}
}

func envOrDefault(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}
