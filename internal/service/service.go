package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/ontheblock/chat-service/internal/domain"
	"github.com/ontheblock/chat-service/internal/id"
	"github.com/ontheblock/chat-service/internal/pubsub"
	"github.com/ontheblock/chat-service/internal/repository"
)

var (
	ErrMemberLeft    = errors.New("member is left")
	ErrMemberRemoved = errors.New("member is removed")
)

const streamCatchUpBatchSize = 100

type ChatService struct {
	tx     repository.TxRunner
	repo   repository.ChatRepository
	pubsub pubsub.RoomPubSub
	now    func() time.Time
}

func New(tx repository.TxRunner, repo repository.ChatRepository, ps pubsub.RoomPubSub) *ChatService {
	return &ChatService{tx: tx, repo: repo, pubsub: ps, now: time.Now}
}

type CreateRoomInput struct {
	CreatorUserID string
	Title         string
}

func (s *ChatService) CreateRoom(ctx context.Context, in CreateRoomInput) (domain.ChatRoom, error) {
	now := s.now().UTC()
	room := domain.ChatRoom{
		ID:          id.New(),
		RoomType:    domain.RoomTypeGeneralGroup,
		Title:       strings.TrimSpace(in.Title),
		OwnerUserID: in.CreatorUserID,
		IsActive:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	member := domain.ChatRoomMember{
		ID:        id.New(),
		RoomID:    room.ID,
		UserID:    in.CreatorUserID,
		Role:      domain.MemberRoleOwner,
		Status:    domain.MemberStatusActive,
		JoinedAt:  now,
		CreatedAt: now,
		UpdatedAt: now,
	}
	err := s.tx.WithTx(ctx, func(ctx context.Context, repo repository.ChatRepository) error {
		if err := repo.CreateRoom(ctx, room); err != nil {
			return err
		}
		return repo.CreateMember(ctx, member)
	})
	if err != nil {
		return domain.ChatRoom{}, err
	}
	return room, nil
}

type CreateBoardLinkedRoomInput struct {
	CreatorUserID string
	BoardID       string
	Title         string
}

func (s *ChatService) CreateBoardLinkedRoom(ctx context.Context, in CreateBoardLinkedRoomInput) (domain.ChatRoom, bool, error) {
	if _, err := s.repo.GetActiveBoardLinkedRoom(ctx, in.BoardID); err == nil {
		return domain.ChatRoom{}, true, nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return domain.ChatRoom{}, false, err
	}

	now := s.now().UTC()
	room := domain.ChatRoom{
		ID:            id.New(),
		RoomType:      domain.RoomTypeBoardLinkedGroup,
		Title:         strings.TrimSpace(in.Title),
		LinkedBoardID: in.BoardID,
		OwnerUserID:   in.CreatorUserID,
		IsActive:      true,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	member := domain.ChatRoomMember{
		ID:        id.New(),
		RoomID:    room.ID,
		UserID:    in.CreatorUserID,
		Role:      domain.MemberRoleOwner,
		Status:    domain.MemberStatusActive,
		JoinedAt:  now,
		CreatedAt: now,
		UpdatedAt: now,
	}
	err := s.tx.WithTx(ctx, func(ctx context.Context, repo repository.ChatRepository) error {
		if err := repo.CreateRoom(ctx, room); err != nil {
			if errors.Is(err, domain.ErrAlreadyExists) {
				return err
			}
			return err
		}
		return repo.CreateMember(ctx, member)
	})
	if err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			return domain.ChatRoom{}, true, nil
		}
		return domain.ChatRoom{}, false, err
	}
	return room, false, nil
}

func (s *ChatService) JoinRoom(ctx context.Context, roomID, userID string) (domain.ChatRoomMember, error) {
	room, err := s.repo.GetRoom(ctx, roomID)
	if err != nil {
		return domain.ChatRoomMember{}, err
	}
	if !room.IsActive || room.DeletedAt != nil {
		return domain.ChatRoomMember{}, domain.ErrRoomInactive
	}

	m, err := s.repo.GetMember(ctx, roomID, userID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return domain.ChatRoomMember{}, err
	}
	now := s.now().UTC()
	if err == nil {
		switch m.Status {
		case domain.MemberStatusRemoved:
			return domain.ChatRoomMember{}, domain.ErrRemovedCannotRejoin
		case domain.MemberStatusActive:
			return m, nil
		case domain.MemberStatusLeft:
			m.Status = domain.MemberStatusActive
			m.LeftAt = nil
			m.JoinedAt = now
			m.UpdatedAt = now
			if err := s.repo.UpdateMember(ctx, m); err != nil {
				return domain.ChatRoomMember{}, err
			}
			return m, nil
		}
	}

	newMember := domain.ChatRoomMember{
		ID:        id.New(),
		RoomID:    roomID,
		UserID:    userID,
		Role:      domain.MemberRoleMember,
		Status:    domain.MemberStatusActive,
		JoinedAt:  now,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.repo.CreateMember(ctx, newMember); err != nil {
		return domain.ChatRoomMember{}, err
	}
	return newMember, nil
}

func (s *ChatService) LeaveRoom(ctx context.Context, roomID, userID string) (domain.ChatRoomMember, domain.ChatRoom, error) {
	now := s.now().UTC()
	var outMember domain.ChatRoomMember
	var outRoom domain.ChatRoom

	err := s.tx.WithTx(ctx, func(ctx context.Context, repo repository.ChatRepository) error {
		room, err := repo.GetRoom(ctx, roomID)
		if err != nil {
			return err
		}
		member, err := repo.GetMember(ctx, roomID, userID)
		if err != nil {
			return err
		}
		if member.Status != domain.MemberStatusActive {
			return domain.ErrInvalidState
		}

		member.Status = domain.MemberStatusLeft
		member.LeftAt = &now
		member.UpdatedAt = now
		if member.Role == domain.MemberRoleOwner {
			member.Role = domain.MemberRoleMember
		}
		if err := repo.UpdateMember(ctx, member); err != nil {
			return err
		}
		outMember = member

		if room.OwnerUserID != userID {
			outRoom = room
			return nil
		}

		activeMembers, err := repo.ListActiveMembersByJoinOrder(ctx, roomID)
		if err != nil {
			return err
		}
		if len(activeMembers) == 0 {
			room.IsActive = false
			room.DeletedAt = &now
			room.UpdatedAt = now
			if err := repo.UpdateRoom(ctx, room); err != nil {
				return err
			}
			outRoom = room
			return nil
		}

		sort.SliceStable(activeMembers, func(i, j int) bool {
			if activeMembers[i].JoinedAt.Equal(activeMembers[j].JoinedAt) {
				return activeMembers[i].ID < activeMembers[j].ID
			}
			return activeMembers[i].JoinedAt.Before(activeMembers[j].JoinedAt)
		})
		newOwner := activeMembers[0]
		newOwner.Role = domain.MemberRoleOwner
		newOwner.UpdatedAt = now
		if err := repo.UpdateMember(ctx, newOwner); err != nil {
			return err
		}
		room.OwnerUserID = newOwner.UserID
		room.UpdatedAt = now
		if err := repo.UpdateRoom(ctx, room); err != nil {
			return err
		}
		outRoom = room
		return nil
	})
	if err != nil {
		return domain.ChatRoomMember{}, domain.ChatRoom{}, err
	}
	return outMember, outRoom, nil
}

func (s *ChatService) RemoveMember(ctx context.Context, roomID, ownerUserID, targetUserID string) (domain.ChatRoomMember, error) {
	if targetUserID == ownerUserID {
		return domain.ChatRoomMember{}, domain.ErrPermissionDenied
	}
	now := s.now().UTC()
	var out domain.ChatRoomMember
	err := s.tx.WithTx(ctx, func(ctx context.Context, repo repository.ChatRepository) error {
		room, err := repo.GetRoom(ctx, roomID)
		if err != nil {
			return err
		}
		if room.OwnerUserID != ownerUserID {
			return domain.ErrPermissionDenied
		}
		if room.OwnerUserID == targetUserID {
			return domain.ErrPermissionDenied
		}
		target, err := repo.GetMember(ctx, roomID, targetUserID)
		if err != nil {
			return err
		}
		target.Status = domain.MemberStatusRemoved
		target.RemovedAt = &now
		target.RemovedByUserID = ownerUserID
		target.LeftAt = nil
		target.UpdatedAt = now
		if err := repo.UpdateMember(ctx, target); err != nil {
			return err
		}
		out = target
		return nil
	})
	if err != nil {
		return domain.ChatRoomMember{}, err
	}
	return out, nil
}

func (s *ChatService) DeactivateRoom(ctx context.Context, roomID, ownerUserID string) (domain.ChatRoom, error) {
	now := s.now().UTC()
	room, err := s.repo.GetRoom(ctx, roomID)
	if err != nil {
		return domain.ChatRoom{}, err
	}
	if room.OwnerUserID != ownerUserID {
		return domain.ChatRoom{}, domain.ErrPermissionDenied
	}
	room.IsActive = false
	room.DeletedAt = &now
	room.UpdatedAt = now
	if err := s.repo.UpdateRoom(ctx, room); err != nil {
		return domain.ChatRoom{}, err
	}
	return room, nil
}

func (s *ChatService) SendMessage(ctx context.Context, roomID, senderUserID string, messageType domain.MessageType, content, imageURL string, metadata map[string]any) (domain.ChatMessage, error) {
	room, member, err := s.validateActiveMembership(ctx, roomID, senderUserID)
	if err != nil {
		return domain.ChatMessage{}, err
	}
	if !room.IsActive || member.Status != domain.MemberStatusActive {
		return domain.ChatMessage{}, domain.ErrInvalidState
	}
	now := s.now().UTC()
	msg := domain.ChatMessage{
		ID:           id.New(),
		RoomID:       roomID,
		SenderUserID: senderUserID,
		MessageType:  messageType,
		Content:      content,
		ImageURL:     imageURL,
		Metadata:     metadata,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	var saved domain.ChatMessage
	err = s.tx.WithTx(ctx, func(ctx context.Context, repo repository.ChatRepository) error {
		var txErr error
		saved, txErr = repo.CreateMessageWithNextSequence(ctx, msg)
		if txErr != nil {
			return txErr
		}
		room.UpdatedAt = now
		return repo.UpdateRoom(ctx, room)
	})
	if err != nil {
		return domain.ChatMessage{}, err
	}
	s.pubsub.Publish(ctx, roomID, saved)
	return saved, nil
}

func (s *ChatService) DeleteMessage(ctx context.Context, roomID, messageID, ownerUserID string) (domain.ChatMessage, error) {
	now := s.now().UTC()
	var out domain.ChatMessage
	err := s.tx.WithTx(ctx, func(ctx context.Context, repo repository.ChatRepository) error {
		room, err := repo.GetRoom(ctx, roomID)
		if err != nil {
			return err
		}
		if room.OwnerUserID != ownerUserID {
			return domain.ErrPermissionDenied
		}
		msg, err := repo.GetMessage(ctx, roomID, messageID)
		if err != nil {
			return err
		}
		msg.IsDeleted = true
		msg.DeletedAt = &now
		msg.DeletedByUserID = ownerUserID
		msg.Content = ""
		msg.ImageURL = ""
		msg.Metadata = nil
		msg.UpdatedAt = now
		if err := repo.UpdateMessage(ctx, msg); err != nil {
			return err
		}
		out = msg
		return nil
	})
	if err != nil {
		return domain.ChatMessage{}, err
	}
	return out, nil
}

func (s *ChatService) GetMessages(ctx context.Context, roomID, userID string, beforeSequence int64, limit int) ([]domain.ChatMessage, int64, error) {
	room, member, err := s.validateActiveMembership(ctx, roomID, userID)
	if err != nil {
		return nil, 0, err
	}
	if !room.IsActive {
		return nil, 0, domain.ErrRoomInactive
	}
	if member.Status != domain.MemberStatusActive {
		if member.Status == domain.MemberStatusRemoved {
			return nil, 0, ErrMemberRemoved
		}
		return nil, 0, ErrMemberLeft
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	msgs, nextCursor, err := s.repo.ListMessagesBefore(ctx, roomID, beforeSequence, limit)
	if err != nil {
		return nil, 0, err
	}
	for i := range msgs {
		if msgs[i].IsDeleted {
			msgs[i].Content = ""
			msgs[i].ImageURL = ""
			msgs[i].Metadata = nil
		}
	}
	return msgs, nextCursor, nil
}

func (s *ChatService) MarkAsRead(ctx context.Context, roomID, userID string, lastReadSequenceNo int64) (domain.ChatRoomMember, error) {
	m, err := s.repo.GetMember(ctx, roomID, userID)
	if err != nil {
		return domain.ChatRoomMember{}, err
	}
	if m.Status != domain.MemberStatusActive {
		return domain.ChatRoomMember{}, domain.ErrInvalidState
	}
	now := s.now().UTC()
	if lastReadSequenceNo > m.LastReadSequenceNo {
		m.LastReadSequenceNo = lastReadSequenceNo
	}
	m.LastReadAt = &now
	m.UpdatedAt = now
	if err := s.repo.UpdateMember(ctx, m); err != nil {
		return domain.ChatRoomMember{}, err
	}
	return m, nil
}

func (s *ChatService) ListMyRooms(ctx context.Context, userID string, limit int, pageToken string) ([]domain.ChatRoomSummary, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.repo.ListRoomsByUser(ctx, userID, limit, pageToken)
}

func (s *ChatService) StreamMessages(ctx context.Context, roomID, userID string, afterSequenceNo int64) (<-chan domain.ChatMessage, <-chan error) {
	out := make(chan domain.ChatMessage, 64)
	errCh := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errCh)

		if _, _, err := s.validateActiveMembership(ctx, roomID, userID); err != nil {
			errCh <- err
			return
		}

		sub := s.pubsub.Subscribe(ctx, roomID, 256)
		defer sub.Close()
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		pending := map[int64]domain.ChatMessage{}
		lastDelivered := afterSequenceNo

		for {
			msgs, err := s.repo.ListMessagesAfter(ctx, roomID, lastDelivered, streamCatchUpBatchSize)
			if err != nil {
				errCh <- err
				return
			}
			for _, msg := range msgs {
				pending[msg.SequenceNo] = msg
			}
			s.drainSubscription(sub.C(), pending)

			var flushErr error
			lastDelivered, flushErr = s.flushPendingMessages(ctx, out, pending, lastDelivered)
			if flushErr != nil {
				return
			}
			if len(msgs) < streamCatchUpBatchSize {
				break
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				member, err := s.repo.GetMember(ctx, roomID, userID)
				if err != nil {
					errCh <- err
					return
				}
				if member.Status == domain.MemberStatusLeft {
					errCh <- ErrMemberLeft
					return
				}
				if member.Status == domain.MemberStatusRemoved {
					errCh <- ErrMemberRemoved
					return
				}
			case msg, ok := <-sub.C():
				if !ok {
					return
				}
				if msg.SequenceNo <= lastDelivered {
					continue
				}
				pending[msg.SequenceNo] = msg
				if msg.SequenceNo > lastDelivered+1 {
					msgs, err := s.repo.ListMessagesAfter(ctx, roomID, lastDelivered, streamCatchUpBatchSize)
					if err != nil {
						errCh <- err
						return
					}
					for _, catchUp := range msgs {
						pending[catchUp.SequenceNo] = catchUp
					}
				}
				var flushErr error
				lastDelivered, flushErr = s.flushPendingMessages(ctx, out, pending, lastDelivered)
				if flushErr != nil {
					return
				}
			}
		}
	}()

	return out, errCh
}

func (s *ChatService) validateActiveMembership(ctx context.Context, roomID, userID string) (domain.ChatRoom, domain.ChatRoomMember, error) {
	room, err := s.repo.GetRoom(ctx, roomID)
	if err != nil {
		return domain.ChatRoom{}, domain.ChatRoomMember{}, err
	}
	if !room.IsActive || room.DeletedAt != nil {
		return domain.ChatRoom{}, domain.ChatRoomMember{}, domain.ErrRoomInactive
	}
	member, err := s.repo.GetMember(ctx, roomID, userID)
	if err != nil {
		return domain.ChatRoom{}, domain.ChatRoomMember{}, err
	}
	return room, member, nil
}

func (s *ChatService) drainSubscription(sub <-chan domain.ChatMessage, pending map[int64]domain.ChatMessage) {
	for {
		select {
		case msg, ok := <-sub:
			if !ok {
				return
			}
			pending[msg.SequenceNo] = msg
		default:
			return
		}
	}
}

func (s *ChatService) flushPendingMessages(ctx context.Context, out chan<- domain.ChatMessage, pending map[int64]domain.ChatMessage, lastDelivered int64) (int64, error) {
	for {
		next := lastDelivered + 1
		msg, ok := pending[next]
		if !ok {
			return lastDelivered, nil
		}
		delete(pending, next)
		select {
		case out <- msg:
			lastDelivered = next
		case <-ctx.Done():
			return lastDelivered, ctx.Err()
		}
	}
}
