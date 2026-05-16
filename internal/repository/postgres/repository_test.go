package postgres

import (
	"context"
	"database/sql"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/ontheblock/chat-service/internal/domain"
	"github.com/ontheblock/chat-service/internal/id"
)

func openTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()

	dsn := os.Getenv("CHAT_SERVICE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set CHAT_SERVICE_TEST_PG_DSN to run postgres repository tests")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("db ping failed: %v", err)
	}

	return New(db), db
}

func TestCreateMessageWithNextSequenceConcurrent(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	room := domain.ChatRoom{
		ID:          id.New(),
		RoomType:    domain.RoomTypeGeneralGroup,
		Title:       "repo-concurrency",
		OwnerUserID: id.New(),
		IsActive:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.CreateRoom(ctx, room); err != nil {
		t.Fatalf("create room failed: %v", err)
	}

	const sends = 24
	results := make(chan int64, sends)
	errs := make(chan error, sends)

	var wg sync.WaitGroup
	for i := 0; i < sends; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			msg, err := store.CreateMessageWithNextSequence(ctx, domain.ChatMessage{
				ID:           id.New(),
				RoomID:       room.ID,
				SenderUserID: room.OwnerUserID,
				MessageType:  domain.MessageTypeText,
				Content:      "message",
				Metadata:     map[string]any{"index": i},
				CreatedAt:    now.Add(time.Duration(i) * time.Millisecond),
				UpdatedAt:    now.Add(time.Duration(i) * time.Millisecond),
			})
			if err != nil {
				errs <- err
				return
			}
			results <- msg.SequenceNo
		}(i)
	}

	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent insert failed: %v", err)
		}
	}

	seqs := make([]int64, 0, sends)
	for seq := range results {
		seqs = append(seqs, seq)
	}
	if len(seqs) != sends {
		t.Fatalf("expected %d sequence numbers, got %d", sends, len(seqs))
	}

	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for i, seq := range seqs {
		expected := int64(i + 1)
		if seq != expected {
			t.Fatalf("expected contiguous sequence numbers 1..%d, got %v", sends, seqs)
		}
	}
}

func TestListRoomsByUserReturnsUnreadCountsAndPagination(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	userID := id.New()
	otherUserID := id.New()

	room1 := domain.ChatRoom{
		ID:          id.New(),
		RoomType:    domain.RoomTypeGeneralGroup,
		Title:       "room-1",
		OwnerUserID: userID,
		IsActive:    true,
		CreatedAt:   now.Add(-2 * time.Hour),
		UpdatedAt:   now.Add(-2 * time.Hour),
	}
	room2 := domain.ChatRoom{
		ID:          id.New(),
		RoomType:    domain.RoomTypeGeneralGroup,
		Title:       "room-2",
		OwnerUserID: userID,
		IsActive:    true,
		CreatedAt:   now.Add(-1 * time.Hour),
		UpdatedAt:   now.Add(-1 * time.Hour),
	}
	for _, room := range []domain.ChatRoom{room1, room2} {
		if err := store.CreateRoom(ctx, room); err != nil {
			t.Fatalf("create room failed: %v", err)
		}
	}

	member1 := domain.ChatRoomMember{
		ID:                 id.New(),
		RoomID:             room1.ID,
		UserID:             userID,
		Role:               domain.MemberRoleOwner,
		Status:             domain.MemberStatusActive,
		JoinedAt:           room1.CreatedAt,
		LastReadSequenceNo: 1,
		CreatedAt:          room1.CreatedAt,
		UpdatedAt:          room1.CreatedAt,
	}
	member2 := domain.ChatRoomMember{
		ID:                 id.New(),
		RoomID:             room2.ID,
		UserID:             userID,
		Role:               domain.MemberRoleOwner,
		Status:             domain.MemberStatusActive,
		JoinedAt:           room2.CreatedAt,
		LastReadSequenceNo: 0,
		CreatedAt:          room2.CreatedAt,
		UpdatedAt:          room2.CreatedAt,
	}
	for _, member := range []domain.ChatRoomMember{member1, member2} {
		if err := store.CreateMember(ctx, member); err != nil {
			t.Fatalf("create member failed: %v", err)
		}
	}

	for i := 0; i < 2; i++ {
		senderUserID := userID
		if i == 1 {
			senderUserID = otherUserID
		}
		if _, err := store.CreateMessageWithNextSequence(ctx, domain.ChatMessage{
			ID:           id.New(),
			RoomID:       room1.ID,
			SenderUserID: senderUserID,
			MessageType:  domain.MessageTypeText,
			Content:      "r1",
			CreatedAt:    now.Add(time.Duration(i) * time.Second),
			UpdatedAt:    now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("create room1 message failed: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := store.CreateMessageWithNextSequence(ctx, domain.ChatMessage{
			ID:           id.New(),
			RoomID:       room2.ID,
			SenderUserID: otherUserID,
			MessageType:  domain.MessageTypeText,
			Content:      "r2",
			CreatedAt:    now.Add(time.Duration(i) * time.Second),
			UpdatedAt:    now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("create room2 message failed: %v", err)
		}
	}

	rooms, nextToken, err := store.ListRoomsByUser(ctx, userID, 1, "")
	if err != nil {
		t.Fatalf("list rooms failed: %v", err)
	}
	if len(rooms) != 1 {
		t.Fatalf("expected 1 room on first page, got %d", len(rooms))
	}
	if rooms[0].Room.ID != room2.ID {
		t.Fatalf("expected newest updated room first, got %s", rooms[0].Room.ID)
	}
	if rooms[0].UnreadCnt != 3 {
		t.Fatalf("expected unread count 3 for room2, got %d", rooms[0].UnreadCnt)
	}
	if rooms[0].LastMessage == nil {
		t.Fatalf("expected last message for room2")
	}
	if rooms[0].LastMessage.SequenceNo != 3 {
		t.Fatalf("expected last message sequence 3 for room2, got %d", rooms[0].LastMessage.SequenceNo)
	}
	if rooms[0].LastMessage.Content != "r2" {
		t.Fatalf("expected last message content r2 for room2, got %q", rooms[0].LastMessage.Content)
	}
	if nextToken == "" {
		t.Fatalf("expected next page token")
	}

	rooms, nextToken, err = store.ListRoomsByUser(ctx, userID, 1, nextToken)
	if err != nil {
		t.Fatalf("list rooms second page failed: %v", err)
	}
	if len(rooms) != 1 {
		t.Fatalf("expected 1 room on second page, got %d", len(rooms))
	}
	if rooms[0].Room.ID != room1.ID {
		t.Fatalf("expected room1 on second page, got %s", rooms[0].Room.ID)
	}
	if rooms[0].UnreadCnt != 1 {
		t.Fatalf("expected unread count 1 for room1, got %d", rooms[0].UnreadCnt)
	}
	if rooms[0].LastMessage == nil {
		t.Fatalf("expected last message for room1")
	}
	if rooms[0].LastMessage.SequenceNo != 2 {
		t.Fatalf("expected last message sequence 2 for room1, got %d", rooms[0].LastMessage.SequenceNo)
	}
	if nextToken != "" {
		t.Fatalf("expected empty next token on last page, got %q", nextToken)
	}
}

func TestListRoomsByUserHandlesRoomsWithoutLastMessage(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	userID := id.New()

	room1 := domain.ChatRoom{
		ID:          id.New(),
		RoomType:    domain.RoomTypeGeneralGroup,
		Title:       "room-with-message",
		OwnerUserID: userID,
		IsActive:    true,
		CreatedAt:   now.Add(-2 * time.Hour),
		UpdatedAt:   now.Add(-2 * time.Hour),
	}
	room2 := domain.ChatRoom{
		ID:          id.New(),
		RoomType:    domain.RoomTypeGeneralGroup,
		Title:       "room-without-message",
		OwnerUserID: userID,
		IsActive:    true,
		CreatedAt:   now.Add(-1 * time.Hour),
		UpdatedAt:   now.Add(-1 * time.Hour),
	}
	for _, room := range []domain.ChatRoom{room1, room2} {
		if err := store.CreateRoom(ctx, room); err != nil {
			t.Fatalf("create room failed: %v", err)
		}
		if err := store.CreateMember(ctx, domain.ChatRoomMember{
			ID:        id.New(),
			RoomID:    room.ID,
			UserID:    userID,
			Role:      domain.MemberRoleOwner,
			Status:    domain.MemberStatusActive,
			JoinedAt:  room.CreatedAt,
			CreatedAt: room.CreatedAt,
			UpdatedAt: room.CreatedAt,
		}); err != nil {
			t.Fatalf("create member failed: %v", err)
		}
	}

	if _, err := store.CreateMessageWithNextSequence(ctx, domain.ChatMessage{
		ID:           id.New(),
		RoomID:       room1.ID,
		SenderUserID: userID,
		MessageType:  domain.MessageTypeText,
		Content:      "hello",
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("create message failed: %v", err)
	}

	rooms, nextToken, err := store.ListRoomsByUser(ctx, userID, 1, "")
	if err != nil {
		t.Fatalf("list rooms first page failed: %v", err)
	}
	if len(rooms) != 1 {
		t.Fatalf("expected 1 room on first page, got %d", len(rooms))
	}
	if rooms[0].Room.ID != room2.ID {
		t.Fatalf("expected newer room without messages first, got %s", rooms[0].Room.ID)
	}
	if rooms[0].LastMessage != nil {
		t.Fatalf("expected nil last message for room without messages")
	}
	if nextToken == "" {
		t.Fatalf("expected next page token")
	}

	rooms, nextToken, err = store.ListRoomsByUser(ctx, userID, 1, nextToken)
	if err != nil {
		t.Fatalf("list rooms second page failed: %v", err)
	}
	if len(rooms) != 1 {
		t.Fatalf("expected 1 room on second page, got %d", len(rooms))
	}
	if rooms[0].Room.ID != room1.ID {
		t.Fatalf("expected room with message on second page, got %s", rooms[0].Room.ID)
	}
	if rooms[0].LastMessage == nil {
		t.Fatalf("expected last message on second page")
	}
	if nextToken != "" {
		t.Fatalf("expected empty next token on last page, got %q", nextToken)
	}
}

func TestDeviceTokenLifecycle(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	userID := id.New()

	token, err := store.UpsertDeviceToken(ctx, domain.DeviceToken{
		UserID:     userID,
		DeviceID:   "device-1",
		Token:      "token-1",
		Platform:   domain.DevicePlatformIOS,
		IsActive:   true,
		CreatedAt:  now,
		UpdatedAt:  now,
		LastSeenAt: now,
	})
	if err != nil {
		t.Fatalf("upsert device token failed: %v", err)
	}
	if token.UserID != userID || token.DeviceID != "device-1" || token.Token != "token-1" || token.Platform != domain.DevicePlatformIOS {
		t.Fatalf("unexpected token after insert: %+v", token)
	}

	updatedAt := now.Add(time.Minute)
	token, err = store.UpsertDeviceToken(ctx, domain.DeviceToken{
		UserID:     userID,
		DeviceID:   "device-1",
		Token:      "token-2",
		Platform:   domain.DevicePlatformAndroid,
		IsActive:   true,
		CreatedAt:  updatedAt,
		UpdatedAt:  updatedAt,
		LastSeenAt: updatedAt,
	})
	if err != nil {
		t.Fatalf("upsert existing device token failed: %v", err)
	}
	if token.Token != "token-2" || token.Platform != domain.DevicePlatformAndroid {
		t.Fatalf("expected token update, got %+v", token)
	}

	active, err := store.ListActiveDeviceTokensByUserIDs(ctx, []string{userID})
	if err != nil {
		t.Fatalf("list active tokens failed: %v", err)
	}
	if len(active) != 1 || active[0].Token != "token-2" {
		t.Fatalf("expected one updated active token, got %+v", active)
	}

	otherUserID := id.New()
	if _, err := store.UpsertDeviceToken(ctx, domain.DeviceToken{
		UserID:     otherUserID,
		DeviceID:   "device-2",
		Token:      "token-2",
		Platform:   domain.DevicePlatformAndroid,
		IsActive:   true,
		CreatedAt:  updatedAt,
		UpdatedAt:  updatedAt,
		LastSeenAt: updatedAt,
	}); err != nil {
		t.Fatalf("upsert same token for another user failed: %v", err)
	}
	active, err = store.ListActiveDeviceTokensByUserIDs(ctx, []string{userID})
	if err != nil {
		t.Fatalf("list original user active tokens failed: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("expected original user token to be deactivated by same-token upsert, got %+v", active)
	}
	active, err = store.ListActiveDeviceTokensByUserIDs(ctx, []string{otherUserID})
	if err != nil {
		t.Fatalf("list other user active tokens failed: %v", err)
	}
	if len(active) != 1 || active[0].Token != "token-2" {
		t.Fatalf("expected other user token to stay active, got %+v", active)
	}

	if err := store.DeactivateDeviceToken(ctx, otherUserID, "device-2", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("deactivate device token failed: %v", err)
	}
	active, err = store.ListActiveDeviceTokensByUserIDs(ctx, []string{otherUserID})
	if err != nil {
		t.Fatalf("list active tokens after deactivate failed: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("expected no active tokens after deactivate, got %+v", active)
	}
}
