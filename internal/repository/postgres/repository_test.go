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
		if _, err := store.CreateMessageWithNextSequence(ctx, domain.ChatMessage{
			ID:           id.New(),
			RoomID:       room1.ID,
			SenderUserID: userID,
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
			SenderUserID: userID,
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
	if nextToken != "" {
		t.Fatalf("expected empty next token on last page, got %q", nextToken)
	}
}
