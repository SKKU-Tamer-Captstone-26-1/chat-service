package grpc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/ontheblock/chat-service/internal/pubsub"
	"github.com/ontheblock/chat-service/internal/repository/memory"
	"github.com/ontheblock/chat-service/internal/service"
	chatv1 "github.com/ontheblock/chat-service/proto/chat/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

func startTestGRPCServer(t *testing.T) (chatv1.ChatServiceClient, func()) {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	g := grpc.NewServer()

	store := memory.NewStore()
	svc := service.New(store, store, pubsub.NewMemoryRoomPubSub())
	chatv1.RegisterChatServiceServer(g, NewServer(svc))

	go func() {
		_ = g.Serve(lis)
	}()

	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(dialer), grpc.WithInsecure())
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	cleanup := func() {
		_ = conn.Close()
		g.Stop()
		_ = lis.Close()
	}
	return chatv1.NewChatServiceClient(conn), cleanup
}

func TestGRPCRoomMessageFlow(t *testing.T) {
	client, cleanup := startTestGRPCServer(t)
	defer cleanup()
	ctx := context.Background()

	createResp, err := client.CreateRoom(ctx, &chatv1.CreateRoomRequest{CreatorUserId: "owner", Title: "room"})
	if err != nil {
		t.Fatalf("create room failed: %v", err)
	}
	roomID := createResp.GetRoom().GetRoomId()

	if _, err := client.JoinRoom(ctx, &chatv1.JoinRoomRequest{RoomId: roomID, UserId: "member1"}); err != nil {
		t.Fatalf("join failed: %v", err)
	}

	sendResp, err := client.SendMessage(ctx, &chatv1.SendMessageRequest{RoomId: roomID, SenderUserId: "owner", MessageType: chatv1.MessageType_MESSAGE_TYPE_TEXT, Content: "hello"})
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	if _, err := client.DeleteMessage(ctx, &chatv1.DeleteMessageRequest{RoomId: roomID, MessageId: sendResp.GetMessage().GetMessageId(), OwnerUserId: "owner"}); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	msgsResp, err := client.GetMessages(ctx, &chatv1.GetMessagesRequest{RoomId: roomID, UserId: "owner", Limit: 20})
	if err != nil {
		t.Fatalf("get messages failed: %v", err)
	}
	if len(msgsResp.GetMessages()) != 1 {
		t.Fatalf("expected one message")
	}
	m := msgsResp.GetMessages()[0]
	if !m.GetIsDeleted() {
		t.Fatalf("expected deleted placeholder message")
	}
	if m.GetContent() != "" || m.GetImageUrl() != "" || m.GetMetadata() != nil {
		t.Fatalf("expected deleted content to be sanitized")
	}
}

func TestGRPCStreamTerminatesForLeftMember(t *testing.T) {
	client, cleanup := startTestGRPCServer(t)
	defer cleanup()
	ctx := context.Background()

	createResp, err := client.CreateRoom(ctx, &chatv1.CreateRoomRequest{CreatorUserId: "owner", Title: "room"})
	if err != nil {
		t.Fatalf("create room failed: %v", err)
	}
	roomID := createResp.GetRoom().GetRoomId()

	if _, err := client.JoinRoom(ctx, &chatv1.JoinRoomRequest{RoomId: roomID, UserId: "member1"}); err != nil {
		t.Fatalf("join failed: %v", err)
	}

	stream, err := client.StreamMessages(ctx, &chatv1.StreamMessagesRequest{RoomId: roomID, UserId: "member1"})
	if err != nil {
		t.Fatalf("stream start failed: %v", err)
	}

	if _, err := client.LeaveRoom(ctx, &chatv1.LeaveRoomRequest{RoomId: roomID, UserId: "member1"}); err != nil {
		t.Fatalf("leave failed: %v", err)
	}

	_, err = stream.Recv()
	if err == nil {
		t.Fatalf("expected stream to terminate")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error, got %v", err)
	}
	if st.Code() != codes.FailedPrecondition {
		t.Fatalf("expected FAILED_PRECONDITION, got %s", st.Code())
	}
}

func TestGRPCListMyRoomsIncludesLastMessagePreview(t *testing.T) {
	client, cleanup := startTestGRPCServer(t)
	defer cleanup()
	ctx := context.Background()

	createResp, err := client.CreateRoom(ctx, &chatv1.CreateRoomRequest{CreatorUserId: "owner", Title: "room"})
	if err != nil {
		t.Fatalf("create room failed: %v", err)
	}
	roomID := createResp.GetRoom().GetRoomId()

	if _, err := client.SendMessage(ctx, &chatv1.SendMessageRequest{
		RoomId:       roomID,
		SenderUserId: "owner",
		MessageType:  chatv1.MessageType_MESSAGE_TYPE_TEXT,
		Content:      "latest hello",
	}); err != nil {
		t.Fatalf("send failed: %v", err)
	}

	listResp, err := client.ListMyRooms(ctx, &chatv1.ListMyRoomsRequest{UserId: "owner"})
	if err != nil {
		t.Fatalf("list rooms failed: %v", err)
	}
	if len(listResp.GetRooms()) != 1 {
		t.Fatalf("expected one room, got %d", len(listResp.GetRooms()))
	}
	last := listResp.GetRooms()[0].GetLastMessage()
	if last == nil {
		t.Fatalf("expected last_message preview")
	}
	if last.GetContentPreview() != "latest hello" {
		t.Fatalf("expected content preview latest hello, got %q", last.GetContentPreview())
	}
	if last.GetSequenceNo() != 1 {
		t.Fatalf("expected sequence 1, got %d", last.GetSequenceNo())
	}
	if last.GetSenderUserId() != "owner" {
		t.Fatalf("expected sender owner, got %q", last.GetSenderUserId())
	}
}

func TestGRPCSendImageMessagePersistsImageFields(t *testing.T) {
	client, cleanup := startTestGRPCServer(t)
	defer cleanup()
	ctx := context.Background()

	createResp, err := client.CreateRoom(ctx, &chatv1.CreateRoomRequest{CreatorUserId: "owner", Title: "room"})
	if err != nil {
		t.Fatalf("create room failed: %v", err)
	}
	roomID := createResp.GetRoom().GetRoomId()

	sendResp, err := client.SendMessage(ctx, &chatv1.SendMessageRequest{
		RoomId:       roomID,
		SenderUserId: "owner",
		MessageType:  chatv1.MessageType_MESSAGE_TYPE_IMAGE,
		ImageUrl:     "https://storage.googleapis.com/bucket/image.png",
	})
	if err != nil {
		t.Fatalf("send image failed: %v", err)
	}
	if sendResp.GetMessage().GetMessageType() != chatv1.MessageType_MESSAGE_TYPE_IMAGE {
		t.Fatalf("expected IMAGE message type, got %s", sendResp.GetMessage().GetMessageType())
	}
	if sendResp.GetMessage().GetImageUrl() != "https://storage.googleapis.com/bucket/image.png" {
		t.Fatalf("expected image_url to round-trip, got %q", sendResp.GetMessage().GetImageUrl())
	}
	if sendResp.GetMessage().GetContent() != "" {
		t.Fatalf("expected empty content for image message, got %q", sendResp.GetMessage().GetContent())
	}

	msgsResp, err := client.GetMessages(ctx, &chatv1.GetMessagesRequest{RoomId: roomID, UserId: "owner", Limit: 20})
	if err != nil {
		t.Fatalf("get messages failed: %v", err)
	}
	if len(msgsResp.GetMessages()) != 1 {
		t.Fatalf("expected one image message")
	}
	if msgsResp.GetMessages()[0].GetMessageType() != chatv1.MessageType_MESSAGE_TYPE_IMAGE {
		t.Fatalf("expected stored IMAGE message type, got %s", msgsResp.GetMessages()[0].GetMessageType())
	}
	if msgsResp.GetMessages()[0].GetImageUrl() != "https://storage.googleapis.com/bucket/image.png" {
		t.Fatalf("expected stored image_url, got %q", msgsResp.GetMessages()[0].GetImageUrl())
	}
}

func TestGRPCListMyRoomsUsesImagePreviewPlaceholder(t *testing.T) {
	client, cleanup := startTestGRPCServer(t)
	defer cleanup()
	ctx := context.Background()

	createResp, err := client.CreateRoom(ctx, &chatv1.CreateRoomRequest{CreatorUserId: "owner", Title: "room"})
	if err != nil {
		t.Fatalf("create room failed: %v", err)
	}
	roomID := createResp.GetRoom().GetRoomId()

	if _, err := client.SendMessage(ctx, &chatv1.SendMessageRequest{
		RoomId:       roomID,
		SenderUserId: "owner",
		MessageType:  chatv1.MessageType_MESSAGE_TYPE_IMAGE,
		ImageUrl:     "https://storage.googleapis.com/bucket/image.png",
	}); err != nil {
		t.Fatalf("send image failed: %v", err)
	}

	listResp, err := client.ListMyRooms(ctx, &chatv1.ListMyRoomsRequest{UserId: "owner"})
	if err != nil {
		t.Fatalf("list rooms failed: %v", err)
	}
	if len(listResp.GetRooms()) != 1 {
		t.Fatalf("expected one room, got %d", len(listResp.GetRooms()))
	}
	last := listResp.GetRooms()[0].GetLastMessage()
	if last == nil {
		t.Fatalf("expected last_message preview")
	}
	if last.GetMessageType() != chatv1.MessageType_MESSAGE_TYPE_IMAGE {
		t.Fatalf("expected image preview type, got %s", last.GetMessageType())
	}
	if last.GetContentPreview() != "[Image]" {
		t.Fatalf("expected image placeholder preview, got %q", last.GetContentPreview())
	}
}

func TestGRPCStreamDeliversImageMessageLive(t *testing.T) {
	client, cleanup := startTestGRPCServer(t)
	defer cleanup()
	ctx := context.Background()

	createResp, err := client.CreateRoom(ctx, &chatv1.CreateRoomRequest{CreatorUserId: "owner", Title: "room"})
	if err != nil {
		t.Fatalf("create room failed: %v", err)
	}
	roomID := createResp.GetRoom().GetRoomId()

	stream, err := client.StreamMessages(ctx, &chatv1.StreamMessagesRequest{RoomId: roomID, UserId: "owner"})
	if err != nil {
		t.Fatalf("stream start failed: %v", err)
	}

	if _, err := client.SendMessage(ctx, &chatv1.SendMessageRequest{
		RoomId:       roomID,
		SenderUserId: "owner",
		MessageType:  chatv1.MessageType_MESSAGE_TYPE_IMAGE,
		ImageUrl:     "https://storage.googleapis.com/bucket/live-image.png",
	}); err != nil {
		t.Fatalf("send image failed: %v", err)
	}

	recvCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	done := make(chan *chatv1.StreamMessagesResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := stream.Recv()
		if err != nil {
			errCh <- err
			return
		}
		done <- resp
	}()

	select {
	case resp := <-done:
		msg := resp.GetMessage()
		if msg.GetMessageType() != chatv1.MessageType_MESSAGE_TYPE_IMAGE {
			t.Fatalf("expected IMAGE stream message, got %s", msg.GetMessageType())
		}
		if msg.GetImageUrl() != "https://storage.googleapis.com/bucket/live-image.png" {
			t.Fatalf("expected live image_url, got %q", msg.GetImageUrl())
		}
	case err := <-errCh:
		t.Fatalf("stream recv failed: %v", err)
	case <-recvCtx.Done():
		t.Fatal("timed out waiting for live image message")
	}
}

func TestGRPCSendMessageRejectsInvalidImagePayload(t *testing.T) {
	client, cleanup := startTestGRPCServer(t)
	defer cleanup()
	ctx := context.Background()

	createResp, err := client.CreateRoom(ctx, &chatv1.CreateRoomRequest{CreatorUserId: "owner", Title: "room"})
	if err != nil {
		t.Fatalf("create room failed: %v", err)
	}

	_, err = client.SendMessage(ctx, &chatv1.SendMessageRequest{
		RoomId:       createResp.GetRoom().GetRoomId(),
		SenderUserId: "owner",
		MessageType:  chatv1.MessageType_MESSAGE_TYPE_IMAGE,
	})
	if err == nil {
		t.Fatalf("expected invalid image payload to fail")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error, got %v", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("expected INVALID_ARGUMENT, got %s", st.Code())
	}
}
