package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ontheblock/chat-service/internal/domain"
	"github.com/ontheblock/chat-service/internal/pubsub"
	"github.com/ontheblock/chat-service/internal/repository/memory"
)

func newTestService() (*ChatService, *memory.Store) {
	store := memory.NewStore()
	svc := New(store, store, pubsub.NewMemoryRoomPubSub())
	return svc, store
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
	msg, err := svc.SendMessage(ctx, room.ID, "owner", domain.MessageTypeText, "secret", "http://x", map[string]any{"k": "v"})
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
