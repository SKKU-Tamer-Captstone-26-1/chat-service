package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	chatv1 "github.com/ontheblock/chat-service/proto/chat/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	addr := flag.String("addr", envOrDefault("CHAT_SERVICE_SMOKE_ADDR", "localhost:9090"), "chat-service grpc address")
	mode := flag.String("mode", "smoke", "mode: smoke or chat")

	ownerID := flag.String("owner", "11111111-1111-1111-1111-111111111111", "owner user id (uuid)")
	memberID := flag.String("member", "22222222-2222-2222-2222-222222222222", "member user id (uuid)")
	member2ID := flag.String("member2", "33333333-3333-3333-3333-333333333333", "second member user id (uuid)")

	roomID := flag.String("room", "", "existing room id for chat mode")
	userID := flag.String("user", "33333333-3333-3333-3333-333333333333", "chat-mode user id (uuid)")
	join := flag.Bool("join", true, "join the room before chatting in chat mode")
	message := flag.String("message", "", "send one message and exit in chat mode")
	afterSequenceNo := flag.Int64("after-seq", 0, "stream messages after this sequence number in chat mode")
	flag.Parse()

	connCtx, connCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer connCancel()

	conn, err := grpc.DialContext(connCtx, *addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	client := chatv1.NewChatServiceClient(conn)

	switch *mode {
	case "smoke":
		runSmoke(client, *addr, *ownerID, *memberID, *member2ID)
	case "chat":
		runChat(client, *roomID, *userID, *join, *message, *afterSequenceNo)
	default:
		log.Fatalf("unsupported mode: %s", *mode)
	}
}

func runSmoke(client chatv1.ChatServiceClient, addr, ownerID, memberID, member2ID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	roomResp, err := client.CreateRoom(ctx, &chatv1.CreateRoomRequest{
		CreatorUserId: ownerID,
		Title:         fmt.Sprintf("smoke-%d", time.Now().Unix()),
	})
	must("CreateRoom", err)
	roomID := roomResp.GetRoom().GetRoomId()
	if roomID == "" {
		log.Fatal("CreateRoom returned empty room_id")
	}

	_, err = client.JoinRoom(ctx, &chatv1.JoinRoomRequest{RoomId: roomID, UserId: memberID})
	must("JoinRoom member1", err)

	_, err = client.JoinRoom(ctx, &chatv1.JoinRoomRequest{RoomId: roomID, UserId: member2ID})
	must("JoinRoom member2", err)

	sendResp, err := client.SendMessage(ctx, &chatv1.SendMessageRequest{
		RoomId:       roomID,
		SenderUserId: ownerID,
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
		UserId: ownerID,
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
		UserId:             ownerID,
		LastReadSequenceNo: sendResp.GetMessage().GetSequenceNo(),
	})
	must("MarkAsRead", err)

	log.Printf("SMOKE PASS addr=%s room_id=%s message_id=%s member1=%s member2=%s", addr, roomID, messageID, memberID, member2ID)
}

func runChat(client chatv1.ChatServiceClient, roomID, userID string, join bool, message string, afterSequenceNo int64) {
	if roomID == "" {
		log.Fatal("chat mode requires -room")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if join {
		joinCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := client.JoinRoom(joinCtx, &chatv1.JoinRoomRequest{RoomId: roomID, UserId: userID})
		cancel()
		must("JoinRoom", err)
	}

	if message != "" {
		sendTextMessage(ctx, client, roomID, userID, message)
		log.Printf("sent as %s to room %s", userID, roomID)
		return
	}

	stream, err := client.StreamMessages(ctx, &chatv1.StreamMessagesRequest{
		RoomId:          roomID,
		UserId:          userID,
		AfterSequenceNo: afterSequenceNo,
	})
	must("StreamMessages", err)

	go func() {
		for {
			resp, err := stream.Recv()
			if err != nil {
				if err == io.EOF || ctx.Err() != nil {
					return
				}
				log.Printf("stream recv failed: %v", err)
				stop()
				return
			}
			msg := resp.GetMessage()
			fmt.Printf("[%d] %s: %s\n", msg.GetSequenceNo(), msg.GetSenderUserId(), renderMessage(msg))
		}
	}()

	log.Printf("chat mode ready room=%s user=%s", roomID, userID)
	log.Printf("type a message and press Enter; Ctrl+C exits")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				log.Printf("stdin read failed: %v", err)
			}
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		sendTextMessage(ctx, client, roomID, userID, line)
	}
}

func sendTextMessage(ctx context.Context, client chatv1.ChatServiceClient, roomID, userID, content string) {
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err := client.SendMessage(sendCtx, &chatv1.SendMessageRequest{
		RoomId:       roomID,
		SenderUserId: userID,
		MessageType:  chatv1.MessageType_MESSAGE_TYPE_TEXT,
		Content:      content,
	})
	must("SendMessage", err)
}

func renderMessage(msg *chatv1.ChatMessage) string {
	if msg.GetIsDeleted() {
		return "[deleted]"
	}
	if msg.GetMessageType() == chatv1.MessageType_MESSAGE_TYPE_IMAGE && msg.GetImageUrl() != "" {
		return "[image] " + msg.GetImageUrl()
	}
	return msg.GetContent()
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
