package memory

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/ontheblock/chat-service/internal/domain"
	"github.com/ontheblock/chat-service/internal/repository"
)

type Store struct {
	mu       sync.RWMutex
	rooms    map[string]domain.ChatRoom
	members  map[string]map[string]domain.ChatRoomMember
	messages map[string]map[string]domain.ChatMessage
	maxSeq   map[string]int64
}

func NewStore() *Store {
	return &Store{
		rooms:    map[string]domain.ChatRoom{},
		members:  map[string]map[string]domain.ChatRoomMember{},
		messages: map[string]map[string]domain.ChatMessage{},
		maxSeq:   map[string]int64{},
	}
}

func (s *Store) WithTx(ctx context.Context, fn func(ctx context.Context, repo repository.ChatRepository) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(ctx, s)
}

func (s *Store) CreateRoom(_ context.Context, room domain.ChatRoom) error {
	if _, ok := s.rooms[room.ID]; ok {
		return domain.ErrAlreadyExists
	}
	if room.RoomType == domain.RoomTypeBoardLinkedGroup && room.LinkedBoardID != "" && room.IsActive && room.DeletedAt == nil {
		for _, r := range s.rooms {
			if r.RoomType == domain.RoomTypeBoardLinkedGroup && r.LinkedBoardID == room.LinkedBoardID && r.IsActive && r.DeletedAt == nil {
				return domain.ErrAlreadyExists
			}
		}
	}
	s.rooms[room.ID] = room
	return nil
}

func (s *Store) GetRoom(_ context.Context, roomID string) (domain.ChatRoom, error) {
	room, ok := s.rooms[roomID]
	if !ok {
		return domain.ChatRoom{}, domain.ErrNotFound
	}
	return room, nil
}

func (s *Store) GetActiveBoardLinkedRoom(_ context.Context, boardID string) (domain.ChatRoom, error) {
	for _, room := range s.rooms {
		if room.RoomType == domain.RoomTypeBoardLinkedGroup && room.LinkedBoardID == boardID && room.IsActive && room.DeletedAt == nil {
			return room, nil
		}
	}
	return domain.ChatRoom{}, domain.ErrNotFound
}

func (s *Store) UpdateRoom(_ context.Context, room domain.ChatRoom) error {
	if _, ok := s.rooms[room.ID]; !ok {
		return domain.ErrNotFound
	}
	s.rooms[room.ID] = room
	return nil
}

func (s *Store) ListRoomsByUser(_ context.Context, userID string, limit int, pageToken string) ([]domain.ChatRoomSummary, string, error) {
	if limit <= 0 {
		limit = 20
	}
	rooms := make([]domain.ChatRoomSummary, 0, len(s.rooms))
	for roomID, userMap := range s.members {
		m, ok := userMap[userID]
		if !ok || m.Status != domain.MemberStatusActive {
			continue
		}
		room, ok := s.rooms[roomID]
		if !ok {
			continue
		}
		unread := int64(0)
		var lastMessage *domain.ChatMessage
		for _, msg := range s.messages[roomID] {
			if msg.SequenceNo > m.LastReadSequenceNo {
				unread++
			}
			if lastMessage == nil || msg.SequenceNo > lastMessage.SequenceNo {
				msgCopy := msg
				if msgCopy.IsDeleted {
					msgCopy.Content = ""
					msgCopy.ImageURL = ""
					msgCopy.Metadata = nil
				}
				lastMessage = &msgCopy
			}
		}
		rooms = append(rooms, domain.ChatRoomSummary{Room: room, LastMessage: lastMessage, UnreadCnt: unread})
	}
	sort.SliceStable(rooms, func(i, j int) bool {
		return rooms[i].Room.UpdatedAt.After(rooms[j].Room.UpdatedAt)
	})

	start := 0
	if pageToken != "" {
		decoded, err := base64.StdEncoding.DecodeString(pageToken)
		if err == nil {
			if idx, convErr := strconv.Atoi(string(decoded)); convErr == nil && idx >= 0 {
				start = idx
			}
		}
	}
	if start >= len(rooms) {
		return nil, "", nil
	}
	end := start + limit
	if end > len(rooms) {
		end = len(rooms)
	}
	nextToken := ""
	if end < len(rooms) {
		nextToken = base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%d", end)))
	}
	return rooms[start:end], nextToken, nil
}

func (s *Store) GetMember(_ context.Context, roomID, userID string) (domain.ChatRoomMember, error) {
	roomMembers, ok := s.members[roomID]
	if !ok {
		return domain.ChatRoomMember{}, domain.ErrNotFound
	}
	m, ok := roomMembers[userID]
	if !ok {
		return domain.ChatRoomMember{}, domain.ErrNotFound
	}
	return m, nil
}

func (s *Store) CreateMember(_ context.Context, member domain.ChatRoomMember) error {
	if _, ok := s.members[member.RoomID]; !ok {
		s.members[member.RoomID] = map[string]domain.ChatRoomMember{}
	}
	if _, ok := s.members[member.RoomID][member.UserID]; ok {
		return domain.ErrAlreadyExists
	}
	s.members[member.RoomID][member.UserID] = member
	return nil
}

func (s *Store) UpdateMember(_ context.Context, member domain.ChatRoomMember) error {
	roomMembers, ok := s.members[member.RoomID]
	if !ok {
		return domain.ErrNotFound
	}
	if _, ok := roomMembers[member.UserID]; !ok {
		return domain.ErrNotFound
	}
	roomMembers[member.UserID] = member
	return nil
}

func (s *Store) ListActiveMembersByJoinOrder(_ context.Context, roomID string) ([]domain.ChatRoomMember, error) {
	roomMembers, ok := s.members[roomID]
	if !ok {
		return nil, nil
	}
	out := make([]domain.ChatRoomMember, 0, len(roomMembers))
	for _, m := range roomMembers {
		if m.Status == domain.MemberStatusActive {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].JoinedAt.Equal(out[j].JoinedAt) {
			return strings.Compare(out[i].ID, out[j].ID) < 0
		}
		return out[i].JoinedAt.Before(out[j].JoinedAt)
	})
	return out, nil
}

func (s *Store) CreateMessageWithNextSequence(_ context.Context, msg domain.ChatMessage) (domain.ChatMessage, error) {
	if _, ok := s.rooms[msg.RoomID]; !ok {
		return domain.ChatMessage{}, domain.ErrNotFound
	}
	seq := s.maxSeq[msg.RoomID] + 1
	s.maxSeq[msg.RoomID] = seq
	msg.SequenceNo = seq
	if _, ok := s.messages[msg.RoomID]; !ok {
		s.messages[msg.RoomID] = map[string]domain.ChatMessage{}
	}
	s.messages[msg.RoomID][msg.ID] = msg
	return msg, nil
}

func (s *Store) GetMessage(_ context.Context, roomID, messageID string) (domain.ChatMessage, error) {
	roomMsgs, ok := s.messages[roomID]
	if !ok {
		return domain.ChatMessage{}, domain.ErrNotFound
	}
	msg, ok := roomMsgs[messageID]
	if !ok {
		return domain.ChatMessage{}, domain.ErrNotFound
	}
	return msg, nil
}

func (s *Store) UpdateMessage(_ context.Context, msg domain.ChatMessage) error {
	roomMsgs, ok := s.messages[msg.RoomID]
	if !ok {
		return domain.ErrNotFound
	}
	if _, ok := roomMsgs[msg.ID]; !ok {
		return domain.ErrNotFound
	}
	roomMsgs[msg.ID] = msg
	return nil
}

func (s *Store) ListMessagesBefore(_ context.Context, roomID string, beforeSequence int64, limit int) ([]domain.ChatMessage, int64, error) {
	roomMsgs, ok := s.messages[roomID]
	if !ok {
		return []domain.ChatMessage{}, 0, nil
	}
	if limit <= 0 {
		limit = 50
	}
	all := make([]domain.ChatMessage, 0, len(roomMsgs))
	for _, msg := range roomMsgs {
		if beforeSequence > 0 && msg.SequenceNo >= beforeSequence {
			continue
		}
		all = append(all, msg)
	}
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].SequenceNo > all[j].SequenceNo
	})
	if len(all) <= limit {
		return all, 0, nil
	}
	page := all[:limit]
	next := page[len(page)-1].SequenceNo
	return page, next, nil
}

func (s *Store) ListMessagesAfter(_ context.Context, roomID string, afterSequence int64, limit int) ([]domain.ChatMessage, error) {
	roomMsgs, ok := s.messages[roomID]
	if !ok {
		return []domain.ChatMessage{}, nil
	}
	if limit <= 0 {
		limit = 100
	}
	all := make([]domain.ChatMessage, 0, len(roomMsgs))
	for _, msg := range roomMsgs {
		if msg.SequenceNo <= afterSequence {
			continue
		}
		all = append(all, msg)
	}
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].SequenceNo < all[j].SequenceNo
	})
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}
