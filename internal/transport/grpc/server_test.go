package grpc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/ontheblock/chat-service/internal/auth"
	"github.com/ontheblock/chat-service/internal/pubsub"
	"github.com/ontheblock/chat-service/internal/repository/memory"
	"github.com/ontheblock/chat-service/internal/service"
	"github.com/ontheblock/chat-service/internal/upload"
	chatv1 "github.com/ontheblock/chat-service/proto/chat/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"
)

const bufSize = 1024 * 1024

func startTestGRPCServer(t *testing.T) (chatv1.ChatServiceClient, func()) {
	return startTestGRPCServerWithOptions(t)
}

func startTestGRPCServerWithOptions(t *testing.T, opts ...service.Option) (chatv1.ChatServiceClient, func()) {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	g := grpc.NewServer()

	store := memory.NewStore()
	svc := service.New(store, store, pubsub.NewMemoryRoomPubSub(), opts...)
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

type fakeUploadSigner struct {
	out upload.AttachmentUpload
	err error
}

func (f fakeUploadSigner) CreateAttachmentUploadURL(_ context.Context, _, _, _, _ string) (upload.AttachmentUpload, error) {
	if f.err != nil {
		return upload.AttachmentUpload{}, f.err
	}
	return f.out, nil
}

type fakeReadSigner struct {
	urlPrefix string
	err       error
}

func (f fakeReadSigner) CreateAttachmentReadURL(_ context.Context, objectName string) (upload.AttachmentRead, error) {
	if f.err != nil {
		return upload.AttachmentRead{}, f.err
	}
	prefix := f.urlPrefix
	if prefix == "" {
		prefix = "https://signed-read/"
	}
	return upload.AttachmentRead{
		ObjectName: objectName,
		ReadURL:    prefix + objectName,
		ExpiresAt:  time.Now().UTC().Add(30 * time.Minute),
	}, nil
}

func TestGRPCRoomMessageFlow(t *testing.T) {
	client, cleanup := startTestGRPCServerWithOptions(t, service.WithTrustedAttachmentBucket("bucket"))
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
	client, cleanup := startTestGRPCServerWithOptions(t, service.WithTrustedAttachmentBucket("bucket"), service.WithAttachmentReadSigner(fakeReadSigner{}))
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
		ImageUrl:     "https://storage.googleapis.com/bucket/chat-attachments/" + roomID + "/image.png",
	})
	if err != nil {
		t.Fatalf("send image failed: %v", err)
	}
	if sendResp.GetMessage().GetMessageType() != chatv1.MessageType_MESSAGE_TYPE_IMAGE {
		t.Fatalf("expected IMAGE message type, got %s", sendResp.GetMessage().GetMessageType())
	}
	if sendResp.GetMessage().GetImageUrl() != "https://signed-read/chat-attachments/"+roomID+"/image.png" {
		t.Fatalf("expected signed image_url, got %q", sendResp.GetMessage().GetImageUrl())
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
	if msgsResp.GetMessages()[0].GetImageUrl() != "https://signed-read/chat-attachments/"+roomID+"/image.png" {
		t.Fatalf("expected signed image_url in history, got %q", msgsResp.GetMessages()[0].GetImageUrl())
	}
}

func TestGRPCListMyRoomsUsesImagePreviewPlaceholder(t *testing.T) {
	client, cleanup := startTestGRPCServerWithOptions(t, service.WithTrustedAttachmentBucket("bucket"))
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
		ImageUrl:     "https://storage.googleapis.com/bucket/chat-attachments/" + roomID + "/image.png",
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
	client, cleanup := startTestGRPCServerWithOptions(t, service.WithTrustedAttachmentBucket("bucket"), service.WithAttachmentReadSigner(fakeReadSigner{}))
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
		ImageUrl:     "https://storage.googleapis.com/bucket/chat-attachments/" + roomID + "/live-image.png",
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
		if msg.GetImageUrl() != "https://signed-read/chat-attachments/"+roomID+"/live-image.png" {
			t.Fatalf("expected signed live image_url, got %q", msg.GetImageUrl())
		}
	case err := <-errCh:
		t.Fatalf("stream recv failed: %v", err)
	case <-recvCtx.Done():
		t.Fatal("timed out waiting for live image message")
	}
}

func TestGRPCCreateImageUploadURL(t *testing.T) {
	expected := upload.AttachmentUpload{
		ObjectName: "chat-images/owner/20260503/image-1.png",
		UploadURL:  "https://signed-upload",
		FileURL:    "https://storage.googleapis.com/bucket/chat-images/owner/20260503/image-1.png",
		ExpiresAt:  time.Now().UTC().Add(15 * time.Minute),
	}
	client, cleanup := startTestGRPCServerWithOptions(t, service.WithAttachmentUploadSigner(fakeUploadSigner{out: expected}))
	defer cleanup()

	createResp, err := client.CreateRoom(context.Background(), &chatv1.CreateRoomRequest{CreatorUserId: "owner", Title: "room"})
	if err != nil {
		t.Fatalf("create room failed: %v", err)
	}

	resp, err := client.CreateImageUploadURL(context.Background(), &chatv1.CreateImageUploadURLRequest{
		UserId:      "owner",
		RoomId:      createResp.GetRoom().GetRoomId(),
		FileName:    "image.png",
		ContentType: "image/png",
	})
	if err != nil {
		t.Fatalf("create image upload url failed: %v", err)
	}
	if resp.GetObjectName() != expected.ObjectName || resp.GetUploadUrl() != expected.UploadURL || resp.GetImageUrl() != expected.FileURL {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestGRPCCreateImageUploadURLRejectsInvalidContentType(t *testing.T) {
	client, cleanup := startTestGRPCServerWithOptions(t, service.WithAttachmentUploadSigner(fakeUploadSigner{}))
	defer cleanup()

	createResp, err := client.CreateRoom(context.Background(), &chatv1.CreateRoomRequest{CreatorUserId: "owner", Title: "room"})
	if err != nil {
		t.Fatalf("create room failed: %v", err)
	}

	_, err = client.CreateImageUploadURL(context.Background(), &chatv1.CreateImageUploadURLRequest{
		UserId:      "owner",
		RoomId:      createResp.GetRoom().GetRoomId(),
		FileName:    "file.pdf",
		ContentType: "application/pdf",
	})
	if err == nil {
		t.Fatalf("expected invalid content type to fail")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error, got %v", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("expected INVALID_ARGUMENT, got %s", st.Code())
	}
}

func TestGRPCCreateAttachmentUploadURL(t *testing.T) {
	expected := upload.AttachmentUpload{
		ObjectName: "chat-attachments/owner/20260503/file-1.pdf",
		UploadURL:  "https://signed-upload",
		FileURL:    "https://storage.googleapis.com/bucket/chat-attachments/owner/20260503/file-1.pdf",
		ExpiresAt:  time.Now().UTC().Add(15 * time.Minute),
	}
	client, cleanup := startTestGRPCServerWithOptions(t, service.WithAttachmentUploadSigner(fakeUploadSigner{out: expected}))
	defer cleanup()

	createResp, err := client.CreateRoom(context.Background(), &chatv1.CreateRoomRequest{CreatorUserId: "owner", Title: "room"})
	if err != nil {
		t.Fatalf("create room failed: %v", err)
	}

	resp, err := client.CreateAttachmentUploadURL(context.Background(), &chatv1.CreateAttachmentUploadURLRequest{
		UserId:      "owner",
		RoomId:      createResp.GetRoom().GetRoomId(),
		FileName:    "file.pdf",
		ContentType: "application/pdf",
	})
	if err != nil {
		t.Fatalf("create attachment upload url failed: %v", err)
	}
	if resp.GetObjectName() != expected.ObjectName || resp.GetUploadUrl() != expected.UploadURL || resp.GetFileUrl() != expected.FileURL {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestGRPCSendFileMessagePersistsFileFields(t *testing.T) {
	client, cleanup := startTestGRPCServerWithOptions(t, service.WithTrustedAttachmentBucket("bucket"), service.WithAttachmentReadSigner(fakeReadSigner{}))
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
		MessageType:  chatv1.MessageType_MESSAGE_TYPE_FILE,
		FileUrl:      "https://storage.googleapis.com/bucket/chat-attachments/" + roomID + "/file.pdf",
		Metadata: mustStruct(t, map[string]any{
			"file_url":     "https://storage.googleapis.com/bucket/chat-attachments/" + roomID + "/file.pdf",
			"file_name":    "file.pdf",
			"content_type": "application/pdf",
		}),
	})
	if err != nil {
		t.Fatalf("send file failed: %v", err)
	}
	if sendResp.GetMessage().GetMessageType() != chatv1.MessageType_MESSAGE_TYPE_FILE {
		t.Fatalf("expected FILE message type, got %s", sendResp.GetMessage().GetMessageType())
	}
	if sendResp.GetMessage().GetFileUrl() != "https://signed-read/chat-attachments/"+roomID+"/file.pdf" {
		t.Fatalf("expected signed file_url, got %q", sendResp.GetMessage().GetFileUrl())
	}

	msgsResp, err := client.GetMessages(ctx, &chatv1.GetMessagesRequest{RoomId: roomID, UserId: "owner", Limit: 20})
	if err != nil {
		t.Fatalf("get messages failed: %v", err)
	}
	if len(msgsResp.GetMessages()) != 1 {
		t.Fatalf("expected one file message")
	}
	if msgsResp.GetMessages()[0].GetMessageType() != chatv1.MessageType_MESSAGE_TYPE_FILE {
		t.Fatalf("expected stored FILE message type, got %s", msgsResp.GetMessages()[0].GetMessageType())
	}
	if msgsResp.GetMessages()[0].GetFileUrl() != "https://signed-read/chat-attachments/"+roomID+"/file.pdf" {
		t.Fatalf("expected signed file_url in history, got %q", msgsResp.GetMessages()[0].GetFileUrl())
	}
}

func TestGRPCListMyRoomsUsesFilePreviewPlaceholder(t *testing.T) {
	client, cleanup := startTestGRPCServerWithOptions(t, service.WithTrustedAttachmentBucket("bucket"))
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
		MessageType:  chatv1.MessageType_MESSAGE_TYPE_FILE,
		FileUrl:      "https://storage.googleapis.com/bucket/chat-attachments/" + roomID + "/spec.pdf",
		Metadata: mustStruct(t, map[string]any{
			"file_url":     "https://storage.googleapis.com/bucket/chat-attachments/" + roomID + "/spec.pdf",
			"file_name":    "spec.pdf",
			"content_type": "application/pdf",
		}),
	}); err != nil {
		t.Fatalf("send file failed: %v", err)
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
	if last.GetMessageType() != chatv1.MessageType_MESSAGE_TYPE_FILE {
		t.Fatalf("expected file preview type, got %s", last.GetMessageType())
	}
	if last.GetContentPreview() != "[File] spec.pdf" {
		t.Fatalf("expected file placeholder preview, got %q", last.GetContentPreview())
	}
}

func TestGRPCSendMessageRejectsInvalidFilePayload(t *testing.T) {
	client, cleanup := startTestGRPCServerWithOptions(t, service.WithTrustedAttachmentBucket("bucket"))
	defer cleanup()
	ctx := context.Background()

	createResp, err := client.CreateRoom(ctx, &chatv1.CreateRoomRequest{CreatorUserId: "owner", Title: "room"})
	if err != nil {
		t.Fatalf("create room failed: %v", err)
	}

	_, err = client.SendMessage(ctx, &chatv1.SendMessageRequest{
		RoomId:       createResp.GetRoom().GetRoomId(),
		SenderUserId: "owner",
		MessageType:  chatv1.MessageType_MESSAGE_TYPE_FILE,
		FileUrl:      "https://storage.googleapis.com/bucket/file.pdf",
	})
	if err == nil {
		t.Fatalf("expected invalid file payload to fail")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error, got %v", err)
	}
	if st.Code() != codes.InvalidArgument {
		t.Fatalf("expected INVALID_ARGUMENT, got %s", st.Code())
	}
}

func TestGRPCCreateAttachmentUploadURLRejectsNonMember(t *testing.T) {
	client, cleanup := startTestGRPCServerWithOptions(t, service.WithAttachmentUploadSigner(fakeUploadSigner{}))
	defer cleanup()

	createResp, err := client.CreateRoom(context.Background(), &chatv1.CreateRoomRequest{CreatorUserId: "owner", Title: "room"})
	if err != nil {
		t.Fatalf("create room failed: %v", err)
	}

	_, err = client.CreateAttachmentUploadURL(context.Background(), &chatv1.CreateAttachmentUploadURLRequest{
		UserId:      "stranger",
		RoomId:      createResp.GetRoom().GetRoomId(),
		FileName:    "file.pdf",
		ContentType: "application/pdf",
	})
	if err == nil {
		t.Fatalf("expected non-member upload URL request to fail")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error, got %v", err)
	}
	if st.Code() != codes.NotFound {
		t.Fatalf("expected NOT_FOUND, got %s", st.Code())
	}
}

func TestGRPCSendMessageRejectsExternalFileURL(t *testing.T) {
	client, cleanup := startTestGRPCServerWithOptions(t, service.WithTrustedAttachmentBucket("bucket"))
	defer cleanup()
	ctx := context.Background()

	createResp, err := client.CreateRoom(ctx, &chatv1.CreateRoomRequest{CreatorUserId: "owner", Title: "room"})
	if err != nil {
		t.Fatalf("create room failed: %v", err)
	}

	_, err = client.SendMessage(ctx, &chatv1.SendMessageRequest{
		RoomId:       createResp.GetRoom().GetRoomId(),
		SenderUserId: "owner",
		MessageType:  chatv1.MessageType_MESSAGE_TYPE_FILE,
		FileUrl:      "https://evil.example/file.pdf",
		Metadata: mustStruct(t, map[string]any{
			"file_url":     "https://evil.example/file.pdf",
			"file_name":    "file.pdf",
			"content_type": "application/pdf",
		}),
	})
	if err == nil {
		t.Fatalf("expected external file URL to fail")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error, got %v", err)
	}
	if st.Code() != codes.PermissionDenied {
		t.Fatalf("expected PERMISSION_DENIED, got %s", st.Code())
	}
}

func mustStruct(t *testing.T, values map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(values)
	if err != nil {
		t.Fatalf("new struct failed: %v", err)
	}
	return s
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

func TestGRPCAuthenticatedUserCanOmitBodyUserID(t *testing.T) {
	store := memory.NewStore()
	svc := service.New(store, store, pubsub.NewMemoryRoomPubSub())
	server := NewServer(svc)
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{UserID: "owner", Role: auth.RoleNormal})

	createResp, err := server.CreateRoom(ctx, &chatv1.CreateRoomRequest{Title: "room"})
	if err != nil {
		t.Fatalf("create room failed: %v", err)
	}

	sendResp, err := server.SendMessage(ctx, &chatv1.SendMessageRequest{
		RoomId:      createResp.GetRoom().GetRoomId(),
		MessageType: chatv1.MessageType_MESSAGE_TYPE_TEXT,
		Content:     "hello",
	})
	if err != nil {
		t.Fatalf("send message failed: %v", err)
	}
	if sendResp.GetMessage().GetSenderUserId() != "owner" {
		t.Fatalf("expected sender from auth principal, got %q", sendResp.GetMessage().GetSenderUserId())
	}
}

func TestGRPCAuthenticatedUserRejectsMismatchedBodyUserID(t *testing.T) {
	store := memory.NewStore()
	svc := service.New(store, store, pubsub.NewMemoryRoomPubSub())
	server := NewServer(svc)
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{UserID: "owner", Role: auth.RoleNormal})

	_, err := server.CreateRoom(ctx, &chatv1.CreateRoomRequest{CreatorUserId: "someone-else", Title: "room"})
	if err == nil {
		t.Fatal("expected mismatched creator_user_id to fail")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error, got %v", err)
	}
	if st.Code() != codes.PermissionDenied {
		t.Fatalf("expected PERMISSION_DENIED, got %s", st.Code())
	}
}

func TestGRPCGetOrCreateBoardChatRoomUsesAuthenticatedUser(t *testing.T) {
	store := memory.NewStore()
	svc := service.New(store, store, pubsub.NewMemoryRoomPubSub())
	server := NewServer(svc)
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{UserID: "viewer", Role: auth.RoleNormal})

	first, err := server.GetOrCreateBoardChatRoom(ctx, &chatv1.GetOrCreateBoardChatRoomRequest{
		BoardId:          "board-1",
		Title:            "Board room",
		BoardOwnerUserId: "board-owner",
	})
	if err != nil {
		t.Fatalf("first board chat entry failed: %v", err)
	}
	if first.GetAlreadyExists() {
		t.Fatalf("first board chat entry should create a room")
	}
	if first.GetMember().GetUserId() != "viewer" {
		t.Fatalf("expected member from authenticated user, got %q", first.GetMember().GetUserId())
	}
	if first.GetRoom().GetLinkedBoardId() != "board-1" {
		t.Fatalf("expected linked board id board-1, got %q", first.GetRoom().GetLinkedBoardId())
	}

	second, err := server.GetOrCreateBoardChatRoom(ctx, &chatv1.GetOrCreateBoardChatRoomRequest{
		BoardId: "board-1",
		Title:   "Ignored title",
	})
	if err != nil {
		t.Fatalf("second board chat entry failed: %v", err)
	}
	if !second.GetAlreadyExists() {
		t.Fatalf("second board chat entry should return existing room")
	}
	if second.GetRoom().GetRoomId() != first.GetRoom().GetRoomId() {
		t.Fatalf("expected same room id, got %q and %q", first.GetRoom().GetRoomId(), second.GetRoom().GetRoomId())
	}
	if _, err := store.GetMember(context.Background(), first.GetRoom().GetRoomId(), "board-owner"); err != nil {
		t.Fatalf("expected board owner member: %v", err)
	}
}

func TestGRPCGetOrCreateBoardChatRoomRequiresAuthenticatedUser(t *testing.T) {
	store := memory.NewStore()
	svc := service.New(store, store, pubsub.NewMemoryRoomPubSub())
	server := NewServer(svc)

	_, err := server.GetOrCreateBoardChatRoom(context.Background(), &chatv1.GetOrCreateBoardChatRoomRequest{
		BoardId: "board-1",
	})
	if err == nil {
		t.Fatal("expected unauthenticated request to fail")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error, got %v", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Fatalf("expected UNAUTHENTICATED, got %s", st.Code())
	}
}

func TestGRPCMarkChatRoomReadUsesAuthenticatedUser(t *testing.T) {
	store := memory.NewStore()
	svc := service.New(store, store, pubsub.NewMemoryRoomPubSub())
	server := NewServer(svc)
	ownerCtx := auth.WithPrincipal(context.Background(), auth.Principal{UserID: "owner", Role: auth.RoleNormal})
	memberCtx := auth.WithPrincipal(context.Background(), auth.Principal{UserID: "member", Role: auth.RoleNormal})

	createResp, err := server.CreateRoom(ownerCtx, &chatv1.CreateRoomRequest{Title: "room"})
	if err != nil {
		t.Fatalf("create room failed: %v", err)
	}
	roomID := createResp.GetRoom().GetRoomId()
	if _, err := server.JoinRoom(memberCtx, &chatv1.JoinRoomRequest{RoomId: roomID}); err != nil {
		t.Fatalf("join room failed: %v", err)
	}
	if _, err := server.SendMessage(ownerCtx, &chatv1.SendMessageRequest{
		RoomId:      roomID,
		MessageType: chatv1.MessageType_MESSAGE_TYPE_TEXT,
		Content:     "hello",
	}); err != nil {
		t.Fatalf("send message failed: %v", err)
	}

	listResp, err := server.ListMyRooms(memberCtx, &chatv1.ListMyRoomsRequest{})
	if err != nil {
		t.Fatalf("list rooms failed: %v", err)
	}
	if len(listResp.GetRooms()) != 1 || listResp.GetRooms()[0].GetUnreadCount() != 1 {
		t.Fatalf("expected member unread count 1, got %+v", listResp.GetRooms())
	}

	readResp, err := server.MarkChatRoomRead(memberCtx, &chatv1.MarkChatRoomReadRequest{RoomId: roomID})
	if err != nil {
		t.Fatalf("mark chat room read failed: %v", err)
	}
	if readResp.GetUserId() != "member" || readResp.GetLastReadSequenceNo() != 1 {
		t.Fatalf("unexpected mark read response: %+v", readResp)
	}

	listResp, err = server.ListMyRooms(memberCtx, &chatv1.ListMyRoomsRequest{})
	if err != nil {
		t.Fatalf("list rooms after mark read failed: %v", err)
	}
	if len(listResp.GetRooms()) != 1 || listResp.GetRooms()[0].GetUnreadCount() != 0 {
		t.Fatalf("expected unread count reset to 0, got %+v", listResp.GetRooms())
	}
}

func TestGRPCRegisterDeviceTokenUsesAuthenticatedUser(t *testing.T) {
	store := memory.NewStore()
	svc := service.New(store, store, pubsub.NewMemoryRoomPubSub())
	server := NewServer(svc)
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{UserID: "user-1", Role: auth.RoleNormal})

	resp, err := server.RegisterDeviceToken(ctx, &chatv1.RegisterDeviceTokenRequest{
		DeviceId: "device-1",
		Token:    "fcm-token",
		Platform: chatv1.DevicePlatform_DEVICE_PLATFORM_ANDROID,
	})
	if err != nil {
		t.Fatalf("register device token failed: %v", err)
	}
	if resp.GetDeviceToken().GetUserId() != "user-1" {
		t.Fatalf("expected authenticated user id, got %q", resp.GetDeviceToken().GetUserId())
	}

	active, err := store.ListActiveDeviceTokensByUserIDs(context.Background(), []string{"user-1"})
	if err != nil {
		t.Fatalf("list active tokens failed: %v", err)
	}
	if len(active) != 1 || active[0].Token != "fcm-token" {
		t.Fatalf("expected one active registered token, got %+v", active)
	}

	if _, err := server.UnregisterDeviceToken(ctx, &chatv1.UnregisterDeviceTokenRequest{DeviceId: "device-1"}); err != nil {
		t.Fatalf("unregister device token failed: %v", err)
	}
	active, err = store.ListActiveDeviceTokensByUserIDs(context.Background(), []string{"user-1"})
	if err != nil {
		t.Fatalf("list active tokens after unregister failed: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("expected token to be inactive after unregister, got %+v", active)
	}
}

func TestGRPCRegisterDeviceTokenRequiresAuthenticatedUser(t *testing.T) {
	store := memory.NewStore()
	svc := service.New(store, store, pubsub.NewMemoryRoomPubSub())
	server := NewServer(svc)

	_, err := server.RegisterDeviceToken(context.Background(), &chatv1.RegisterDeviceTokenRequest{
		DeviceId: "device-1",
		Token:    "fcm-token",
		Platform: chatv1.DevicePlatform_DEVICE_PLATFORM_IOS,
	})
	if err == nil {
		t.Fatal("expected unauthenticated request to fail")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected grpc status error, got %v", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Fatalf("expected UNAUTHENTICATED, got %s", st.Code())
	}
}
