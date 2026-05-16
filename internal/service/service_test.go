package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ontheblock/chat-service/internal/domain"
	"github.com/ontheblock/chat-service/internal/pubsub"
	"github.com/ontheblock/chat-service/internal/push"
	"github.com/ontheblock/chat-service/internal/repository/memory"
	"github.com/ontheblock/chat-service/internal/upload"
)

func newTestService() (*ChatService, *memory.Store) {
	store := memory.NewStore()
	svc := New(store, store, pubsub.NewMemoryRoomPubSub())
	return svc, store
}

type fakeImageUploadSigner struct {
	out upload.AttachmentUpload
	err error
}

func (f fakeImageUploadSigner) CreateAttachmentUploadURL(_ context.Context, _, _, _, _ string) (upload.AttachmentUpload, error) {
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

type fakePushSender struct {
	mu       sync.Mutex
	messages []push.Message
	err      error
}

func (f *fakePushSender) Send(_ context.Context, msg push.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, msg)
	return f.err
}

func (f *fakePushSender) sentMessages() []push.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]push.Message, len(f.messages))
	copy(out, f.messages)
	return out
}

func TestBoardLinkedRoomUniqueness(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	_, exists, err := svc.CreateBoardLinkedRoom(ctx, CreateBoardLinkedRoomInput{CreatorUserID: "u1", BoardID: "b1", Title: "r1"})
	if err != nil || exists {
		t.Fatalf("first create failed: exists=%v err=%v", exists, err)
	}
	_, exists, err = svc.CreateBoardLinkedRoom(ctx, CreateBoardLinkedRoomInput{CreatorUserID: "u2", BoardID: "b1", Title: "r2"})
	if err != nil {
		t.Fatalf("second create returned error: %v", err)
	}
	if !exists {
		t.Fatalf("expected already exists for board-linked room")
	}
}

func TestGetOrCreateBoardChatRoomReturnsExistingAndAddsEnteringMember(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()

	first, err := svc.GetOrCreateBoardChatRoom(ctx, BoardChatRoomEntryInput{
		UserID:  "u1",
		BoardID: "board-1",
		Title:   "Board room",
	})
	if err != nil {
		t.Fatalf("first entry failed: %v", err)
	}
	if first.AlreadyExists {
		t.Fatalf("first entry should create a new room")
	}
	if first.Room.LinkedBoardID != "board-1" {
		t.Fatalf("expected linked board id board-1, got %q", first.Room.LinkedBoardID)
	}

	second, err := svc.GetOrCreateBoardChatRoom(ctx, BoardChatRoomEntryInput{
		UserID:  "u2",
		BoardID: "board-1",
		Title:   "Ignored title",
	})
	if err != nil {
		t.Fatalf("second entry failed: %v", err)
	}
	if !second.AlreadyExists {
		t.Fatalf("second entry should return existing room")
	}
	if second.Room.ID != first.Room.ID {
		t.Fatalf("expected same room id, got %q and %q", first.Room.ID, second.Room.ID)
	}
	if second.Member.UserID != "u2" || second.Member.Status != domain.MemberStatusActive {
		t.Fatalf("expected u2 to be added as active member, got %+v", second.Member)
	}
	rooms, _, err := store.ListRoomsByUser(ctx, "u2", 20, "")
	if err != nil {
		t.Fatalf("list rooms failed: %v", err)
	}
	if len(rooms) != 1 || rooms[0].Room.ID != first.Room.ID {
		t.Fatalf("expected one joined room for u2, got %+v", rooms)
	}
}

func TestGetOrCreateBoardChatRoomAddsBoardOwnerWhenProvided(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()

	entry, err := svc.GetOrCreateBoardChatRoom(ctx, BoardChatRoomEntryInput{
		UserID:           "viewer",
		BoardID:          "board-2",
		BoardOwnerUserID: "board-owner",
		Title:            "Board room",
	})
	if err != nil {
		t.Fatalf("entry failed: %v", err)
	}
	ownerMember, err := store.GetMember(ctx, entry.Room.ID, "board-owner")
	if err != nil {
		t.Fatalf("expected board owner member: %v", err)
	}
	if ownerMember.Status != domain.MemberStatusActive {
		t.Fatalf("expected active board owner member, got %+v", ownerMember)
	}
}

func TestGetOrCreateBoardChatRoomRequiresUserAndBoardContext(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	_, err := svc.GetOrCreateBoardChatRoom(ctx, BoardChatRoomEntryInput{UserID: "u1"})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("expected invalid argument for missing board, got %v", err)
	}
	_, err = svc.GetOrCreateBoardChatRoom(ctx, BoardChatRoomEntryInput{BoardID: "board-1"})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("expected invalid argument for missing user, got %v", err)
	}
}

func TestUnreadCountsExcludeOwnMessagesAndMarkChatRoomRead(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinRoom(ctx, room.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeText, "hello", "", nil); err != nil {
		t.Fatal(err)
	}

	memberRooms, _, err := svc.ListMyRooms(ctx, "member", 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(memberRooms) != 1 || memberRooms[0].UnreadCnt != 1 {
		t.Fatalf("expected member unread count 1, got %+v", memberRooms)
	}
	ownerRooms, _, err := svc.ListMyRooms(ctx, "owner", 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ownerRooms) != 1 || ownerRooms[0].UnreadCnt != 0 {
		t.Fatalf("expected owner unread count 0 for own message, got %+v", ownerRooms)
	}

	member, err := svc.MarkChatRoomRead(ctx, room.ID, "member")
	if err != nil {
		t.Fatal(err)
	}
	if member.LastReadSequenceNo != 1 {
		t.Fatalf("expected member last read sequence 1, got %d", member.LastReadSequenceNo)
	}
	memberRooms, _, err = svc.ListMyRooms(ctx, "member", 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(memberRooms) != 1 || memberRooms[0].UnreadCnt != 0 {
		t.Fatalf("expected member unread count reset to 0, got %+v", memberRooms)
	}

	if _, err := svc.SendMessage(ctx, room.ID, "member", domain.MessageTypeText, "reply", "", nil); err != nil {
		t.Fatal(err)
	}
	memberRooms, _, err = svc.ListMyRooms(ctx, "member", 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(memberRooms) != 1 || memberRooms[0].UnreadCnt != 0 {
		t.Fatalf("expected member own reply not to increase unread count, got %+v", memberRooms)
	}
	ownerRooms, _, err = svc.ListMyRooms(ctx, "owner", 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ownerRooms) != 1 || ownerRooms[0].UnreadCnt != 1 {
		t.Fatalf("expected owner unread count 1 after member reply, got %+v", ownerRooms)
	}
}

func TestRegisterAndUnregisterDeviceToken(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()

	token, err := svc.RegisterDeviceToken(ctx, "user-1", "device-1", "fcm-token", domain.DevicePlatformIOS)
	if err != nil {
		t.Fatalf("register device token failed: %v", err)
	}
	if token.UserID != "user-1" || token.DeviceID != "device-1" || token.Token != "fcm-token" || token.Platform != domain.DevicePlatformIOS {
		t.Fatalf("unexpected registered token: %+v", token)
	}
	if token.CreatedAt.IsZero() || token.UpdatedAt.IsZero() || token.LastSeenAt.IsZero() {
		t.Fatalf("expected token timestamps to be set: %+v", token)
	}

	active, err := store.ListActiveDeviceTokensByUserIDs(ctx, []string{"user-1"})
	if err != nil {
		t.Fatalf("list active device tokens failed: %v", err)
	}
	if len(active) != 1 || active[0].Token != "fcm-token" {
		t.Fatalf("expected one active device token, got %+v", active)
	}

	if err := svc.UnregisterDeviceToken(ctx, "user-1", "device-1"); err != nil {
		t.Fatalf("unregister device token failed: %v", err)
	}
	active, err = store.ListActiveDeviceTokensByUserIDs(ctx, []string{"user-1"})
	if err != nil {
		t.Fatalf("list active device tokens after unregister failed: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("expected no active device tokens after unregister, got %+v", active)
	}
}

func TestRegisterDeviceTokenValidatesInput(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	_, err := svc.RegisterDeviceToken(ctx, "user-1", "device-1", "fcm-token", "")
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("expected invalid argument for missing platform, got %v", err)
	}
	_, err = svc.RegisterDeviceToken(ctx, "", "device-1", "fcm-token", domain.DevicePlatformAndroid)
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("expected invalid argument for missing user, got %v", err)
	}
	_, err = svc.RegisterDeviceToken(ctx, "user-1", "", "fcm-token", domain.DevicePlatformAndroid)
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("expected invalid argument for missing device, got %v", err)
	}
	_, err = svc.RegisterDeviceToken(ctx, "user-1", "device-1", "", domain.DevicePlatformAndroid)
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("expected invalid argument for missing token, got %v", err)
	}
}

func TestRegisterDeviceTokenDeactivatesSameTokenForPreviousUser(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()

	if _, err := svc.RegisterDeviceToken(ctx, "user-1", "device-1", "shared-token", domain.DevicePlatformIOS); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterDeviceToken(ctx, "user-2", "device-2", "shared-token", domain.DevicePlatformAndroid); err != nil {
		t.Fatal(err)
	}

	user1Tokens, err := store.ListActiveDeviceTokensByUserIDs(ctx, []string{"user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(user1Tokens) != 0 {
		t.Fatalf("expected previous user token to be deactivated, got %+v", user1Tokens)
	}
	user2Tokens, err := store.ListActiveDeviceTokensByUserIDs(ctx, []string{"user-2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(user2Tokens) != 1 || user2Tokens[0].Token != "shared-token" {
		t.Fatalf("expected current user token to stay active, got %+v", user2Tokens)
	}
}

func TestSendMessagePushesToOtherActiveMembersOnly(t *testing.T) {
	store := memory.NewStore()
	pushSender := &fakePushSender{}
	svc := New(store, store, pubsub.NewMemoryRoomPubSub(), WithPushSender(pushSender))
	ctx := context.Background()

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinRoom(ctx, room.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterDeviceToken(ctx, "owner", "owner-device", "owner-token", domain.DevicePlatformIOS); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterDeviceToken(ctx, "member", "member-device", "member-token", domain.DevicePlatformAndroid); err != nil {
		t.Fatal(err)
	}

	msg, err := svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeText, "hello member", "", nil)
	if err != nil {
		t.Fatalf("send message failed: %v", err)
	}

	sent := pushSender.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("expected one push message, got %+v", sent)
	}
	if sent[0].Token != "member-token" {
		t.Fatalf("expected member token only, got %+v", sent[0])
	}
	if sent[0].Title != "New message" || sent[0].Body != "hello member" {
		t.Fatalf("unexpected notification text: %+v", sent[0])
	}
	if sent[0].Data["type"] != "chat_message" || sent[0].Data["room_id"] != room.ID || sent[0].Data["message_id"] != msg.ID {
		t.Fatalf("unexpected push data payload: %+v", sent[0].Data)
	}
}

func TestSendMessageDoesNotFailWhenPushSenderFails(t *testing.T) {
	store := memory.NewStore()
	pushSender := &fakePushSender{err: errors.New("fcm unavailable")}
	svc := New(store, store, pubsub.NewMemoryRoomPubSub(), WithPushSender(pushSender))
	ctx := context.Background()

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinRoom(ctx, room.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegisterDeviceToken(ctx, "member", "member-device", "member-token", domain.DevicePlatformAndroid); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeText, "hello", "", nil); err != nil {
		t.Fatalf("send message should not fail when push fails: %v", err)
	}
}

func TestLeftMemberCanRejoin(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	m1, err := svc.JoinRoom(ctx, room.ID, "u2")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.LeaveRoom(ctx, room.ID, "u2"); err != nil {
		t.Fatal(err)
	}
	m2, err := svc.JoinRoom(ctx, room.ID, "u2")
	if err != nil {
		t.Fatal(err)
	}
	if m2.Status != domain.MemberStatusActive {
		t.Fatalf("expected ACTIVE, got %s", m2.Status)
	}
	if !m2.JoinedAt.After(m1.JoinedAt) {
		t.Fatalf("expected joined_at to be updated on rejoin")
	}
	if m2.LeftAt != nil {
		t.Fatalf("expected left_at cleared on rejoin")
	}
}

func TestRemovedMemberCannotRejoin(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinRoom(ctx, room.ID, "u2"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RemoveMember(ctx, room.ID, "owner", "u2"); err != nil {
		t.Fatal(err)
	}
	_, err = svc.JoinRoom(ctx, room.ID, "u2")
	if !errors.Is(err, domain.ErrRemovedCannotRejoin) {
		t.Fatalf("expected ErrRemovedCannotRejoin, got %v", err)
	}
}

func TestOwnerTransferOnLeaveWithTieBreaker(t *testing.T) {
	svc, store := newTestService()
	ctx := context.Background()

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinRoom(ctx, room.ID, "u2"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinRoom(ctx, room.ID, "u3"); err != nil {
		t.Fatal(err)
	}

	m2, _ := store.GetMember(ctx, room.ID, "u2")
	m3, _ := store.GetMember(ctx, room.ID, "u3")
	tie := time.Now().UTC().Add(-1 * time.Hour)
	m2.JoinedAt = tie
	m3.JoinedAt = tie
	m2.ID = "a-member"
	m3.ID = "b-member"
	_ = store.UpdateMember(ctx, m2)
	_ = store.UpdateMember(ctx, m3)

	_, updatedRoom, err := svc.LeaveRoom(ctx, room.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if updatedRoom.OwnerUserID != "u2" {
		t.Fatalf("expected u2 to become owner via member_id tie-breaker, got %s", updatedRoom.OwnerUserID)
	}
}

func TestLastActiveOwnerLeaveDeactivatesRoom(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_, updatedRoom, err := svc.LeaveRoom(ctx, room.ID, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if updatedRoom.IsActive {
		t.Fatalf("expected room to be inactive")
	}
	if updatedRoom.DeletedAt == nil {
		t.Fatalf("expected deleted_at to be set")
	}
}

func TestInactiveRoomBlocksJoinSendGetMessages(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeactivateRoom(ctx, room.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.JoinRoom(ctx, room.ID, "u2"); !errors.Is(err, domain.ErrRoomInactive) {
		t.Fatalf("expected room inactive on join, got %v", err)
	}
	if _, err := svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeText, "hello", "", nil); !errors.Is(err, domain.ErrRoomInactive) {
		t.Fatalf("expected room inactive on send, got %v", err)
	}
	if _, _, err := svc.GetMessages(ctx, room.ID, "owner", 0, 20); !errors.Is(err, domain.ErrRoomInactive) {
		t.Fatalf("expected room inactive on get messages, got %v", err)
	}
}

func TestDeletedMessagesReturnedAsPlaceholder(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeText, "secret", "", map[string]any{"k": "v"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DeleteMessage(ctx, room.ID, msg.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	msgs, _, err := svc.GetMessages(ctx, room.ID, "owner", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message")
	}
	if !msgs[0].IsDeleted {
		t.Fatalf("expected deleted message")
	}
	if msgs[0].Content != "" || msgs[0].ImageURL != "" || msgs[0].Metadata != nil {
		t.Fatalf("expected placeholder-sanitized deleted message")
	}
}

func TestMessageSequenceOrderingAndCursor(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeText, "m", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	page1, next, err := svc.GetMessages(ctx, room.ID, "owner", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || page1[0].SequenceNo != 3 || page1[1].SequenceNo != 2 {
		t.Fatalf("unexpected first page ordering: %+v", page1)
	}
	if next != 2 {
		t.Fatalf("expected next cursor 2, got %d", next)
	}
	page2, next2, err := svc.GetMessages(ctx, room.ID, "owner", next, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 1 || page2[0].SequenceNo != 1 {
		t.Fatalf("unexpected second page ordering: %+v", page2)
	}
	if next2 != 0 {
		t.Fatalf("expected next cursor 0, got %d", next2)
	}
}

func TestSendMessageRejectsEmptyText(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeText, "   ", "", nil)
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}

func TestSendMessageRejectsTextWithImageURL(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeText, "hello", "https://example.com/x.png", nil)
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}

func TestSendMessageRejectsImageWithoutURL(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeImage, "", "", nil)
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}

func TestSendMessageAllowsImageWithURL(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	svc.trustedAttachmentBucket = "bucket"
	svc.attachmentReadSigner = fakeReadSigner{}

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeImage, "", "https://storage.googleapis.com/bucket/chat-attachments/"+room.ID+"/x.png", map[string]any{"width": 100})
	if err != nil {
		t.Fatal(err)
	}
	if msg.MessageType != domain.MessageTypeImage {
		t.Fatalf("expected IMAGE, got %s", msg.MessageType)
	}
	if msg.ImageURL != "https://signed-read/chat-attachments/"+room.ID+"/x.png" {
		t.Fatalf("expected signed read image_url, got %q", msg.ImageURL)
	}
	if got := extractStringMetadata(msg.Metadata, "object_name"); got != "chat-attachments/"+room.ID+"/x.png" {
		t.Fatalf("expected object_name metadata, got %q", got)
	}
}

func TestSendMessageRejectsFileWithoutMetadata(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeFile, "", "", nil)
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}

func TestSendMessageAllowsFileWithMetadata(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	svc.trustedAttachmentBucket = "bucket"
	svc.attachmentReadSigner = fakeReadSigner{}

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeFile, "", "", map[string]any{
		"file_url":     "https://storage.googleapis.com/bucket/chat-attachments/" + room.ID + "/file.pdf",
		"file_name":    "file.pdf",
		"content_type": "application/pdf",
	})
	if err != nil {
		t.Fatal(err)
	}
	if msg.MessageType != domain.MessageTypeFile {
		t.Fatalf("expected FILE, got %s", msg.MessageType)
	}
	if msg.FileURL != "https://signed-read/chat-attachments/"+room.ID+"/file.pdf" {
		t.Fatalf("expected signed read file_url, got %q", msg.FileURL)
	}
	if got := extractStringMetadata(msg.Metadata, "object_name"); got != "chat-attachments/"+room.ID+"/file.pdf" {
		t.Fatalf("expected object_name metadata, got %q", got)
	}
}

func TestGetMessagesHydratesSignedReadURLFromStoredObjectName(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	svc.trustedAttachmentBucket = "bucket"
	svc.attachmentReadSigner = fakeReadSigner{}

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeImage, "", "https://storage.googleapis.com/bucket/chat-attachments/"+room.ID+"/x.png", nil); err != nil {
		t.Fatal(err)
	}

	msgs, _, err := svc.GetMessages(ctx, room.ID, "owner", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].ImageURL != "https://signed-read/chat-attachments/"+room.ID+"/x.png" {
		t.Fatalf("expected signed read image_url, got %q", msgs[0].ImageURL)
	}
}

func TestCreateImageUploadURLRejectsInvalidContentType(t *testing.T) {
	svc, _ := newTestService()
	svc.attachmentUploadSigner = fakeImageUploadSigner{}
	room, err := svc.CreateRoom(context.Background(), CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.CreateImageUploadURL(context.Background(), room.ID, "owner", "file.pdf", "application/pdf")
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}

func TestCreateImageUploadURLReturnsSignedUploadData(t *testing.T) {
	svc, _ := newTestService()
	expected := upload.AttachmentUpload{
		ObjectName: "chat-attachments/room-1/image-1.png",
		UploadURL:  "https://signed-upload",
		FileURL:    "https://storage.googleapis.com/bucket/chat-attachments/room-1/image-1.png",
		ExpiresAt:  time.Now().UTC().Add(15 * time.Minute),
	}
	svc.attachmentUploadSigner = fakeImageUploadSigner{out: expected}

	room, err := svc.CreateRoom(context.Background(), CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.CreateImageUploadURL(context.Background(), room.ID, "owner", "image.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if got.ObjectName != expected.ObjectName || got.UploadURL != expected.UploadURL || got.ImageURL != expected.FileURL {
		t.Fatalf("unexpected signed upload response: %+v", got)
	}
}

func TestCreateAttachmentUploadURLAllowsPDF(t *testing.T) {
	svc, _ := newTestService()
	expected := upload.AttachmentUpload{
		ObjectName: "chat-attachments/room-1/file-1.pdf",
		UploadURL:  "https://signed-upload",
		FileURL:    "https://storage.googleapis.com/bucket/chat-attachments/room-1/file-1.pdf",
		ExpiresAt:  time.Now().UTC().Add(15 * time.Minute),
	}
	svc.attachmentUploadSigner = fakeImageUploadSigner{out: expected}

	room, err := svc.CreateRoom(context.Background(), CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.CreateAttachmentUploadURL(context.Background(), room.ID, "owner", "file.pdf", "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if got.ObjectName != expected.ObjectName || got.UploadURL != expected.UploadURL || got.FileURL != expected.FileURL {
		t.Fatalf("unexpected signed upload response: %+v", got)
	}
}

func TestCreateAttachmentUploadURLRequiresActiveMembership(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	svc.attachmentUploadSigner = fakeImageUploadSigner{}

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateAttachmentUploadURL(ctx, room.ID, "stranger", "file.pdf", "application/pdf"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-member, got %v", err)
	}
}

func TestSendMessageRejectsExternalAttachmentURL(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()
	svc.trustedAttachmentBucket = "bucket"

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeFile, "", "", map[string]any{
		"file_url":     "https://evil.example/file.pdf",
		"file_name":    "file.pdf",
		"content_type": "application/pdf",
	})
	if !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied, got %v", err)
	}
}

type scriptedSubscription struct {
	ch chan domain.ChatMessage
}

func (s *scriptedSubscription) C() <-chan domain.ChatMessage { return s.ch }
func (s *scriptedSubscription) Close()                       {}

type scriptedPubSub struct {
	mu    sync.Mutex
	sub   *scriptedSubscription
	ready chan struct{}
}

func newScriptedPubSub() *scriptedPubSub {
	return &scriptedPubSub{ready: make(chan struct{})}
}

func (p *scriptedPubSub) Publish(_ context.Context, _ string, msg domain.ChatMessage) {
	p.mu.Lock()
	sub := p.sub
	p.mu.Unlock()
	if sub == nil {
		return
	}
	sub.ch <- msg
}

func (p *scriptedPubSub) Subscribe(_ context.Context, _ string, buffer int) pubsub.Subscription {
	if buffer <= 0 {
		buffer = 64
	}
	sub := &scriptedSubscription{ch: make(chan domain.ChatMessage, buffer)}
	p.mu.Lock()
	p.sub = sub
	p.mu.Unlock()
	close(p.ready)
	return sub
}

func (p *scriptedPubSub) waitForSubscription(t *testing.T) {
	t.Helper()
	select {
	case <-p.ready:
	case <-time.After(2 * time.Second):
		t.Fatal("subscription was not established")
	}
}

func TestStreamMessagesCatchUpIsLosslessBeyondOneBatch(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 150; i++ {
		if _, err := svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeText, "m", "", nil); err != nil {
			t.Fatal(err)
		}
	}

	streamCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msgCh, errCh := svc.StreamMessages(streamCtx, room.ID, "owner", 25)
	got := make([]int64, 0, 125)
	timeout := time.After(3 * time.Second)
	for len(got) < 125 {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				t.Fatalf("stream closed early after %d messages", len(got))
			}
			got = append(got, msg.SequenceNo)
		case err := <-errCh:
			if err != nil {
				t.Fatalf("stream returned error: %v", err)
			}
		case <-timeout:
			t.Fatalf("timed out waiting for catch-up messages, got %d", len(got))
		}
	}

	if got[0] != 26 {
		t.Fatalf("expected first replayed sequence 26, got %d", got[0])
	}
	if got[len(got)-1] != 150 {
		t.Fatalf("expected last replayed sequence 150, got %d", got[len(got)-1])
	}
	for i, seq := range got {
		expected := int64(i + 26)
		if seq != expected {
			t.Fatalf("expected contiguous sequence %d, got %d", expected, seq)
		}
	}
}

func TestStreamMessagesBackfillsMissingGapFromRepository(t *testing.T) {
	store := memory.NewStore()
	ps := newScriptedPubSub()
	svc := New(store, store, ps)
	ctx := context.Background()

	room, err := svc.CreateRoom(ctx, CreateRoomInput{CreatorUserID: "owner", Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeText, "m1", "", nil); err != nil {
		t.Fatal(err)
	}

	streamCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msgCh, errCh := svc.StreamMessages(streamCtx, room.ID, "owner", 1)
	ps.waitForSubscription(t)

	now := time.Now().UTC()
	msg2, err := store.CreateMessageWithNextSequence(ctx, domain.ChatMessage{
		ID:           "msg-2",
		RoomID:       room.ID,
		SenderUserID: "owner",
		MessageType:  domain.MessageTypeText,
		Content:      "m2",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err != nil {
		t.Fatal(err)
	}
	msg3, err := store.CreateMessageWithNextSequence(ctx, domain.ChatMessage{
		ID:           "msg-3",
		RoomID:       room.ID,
		SenderUserID: "owner",
		MessageType:  domain.MessageTypeText,
		Content:      "m3",
		CreatedAt:    now.Add(time.Millisecond),
		UpdatedAt:    now.Add(time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}

	ps.Publish(ctx, room.ID, msg3)

	got := make([]int64, 0, 2)
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case msg, ok := <-msgCh:
			if !ok {
				t.Fatalf("stream closed early after %d messages", len(got))
			}
			got = append(got, msg.SequenceNo)
		case err := <-errCh:
			if err != nil {
				t.Fatalf("stream returned error: %v", err)
			}
		case <-timeout:
			t.Fatalf("timed out waiting for live backfill messages, got %v", got)
		}
	}

	if got[0] != msg2.SequenceNo || got[1] != msg3.SequenceNo {
		t.Fatalf("expected backfilled sequences [%d %d], got %v", msg2.SequenceNo, msg3.SequenceNo, got)
	}
}
