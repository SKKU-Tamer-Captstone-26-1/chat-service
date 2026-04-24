package pubsub

import (
	"context"
	"sync"

	"github.com/ontheblock/chat-service/internal/domain"
	"github.com/ontheblock/chat-service/internal/id"
)

type memorySub struct {
	id      string
	ch      chan domain.ChatMessage
	roomID  string
	ps      *MemoryRoomPubSub
	closed  bool
	closeMu sync.Mutex
}

func (s *memorySub) C() <-chan domain.ChatMessage { return s.ch }

func (s *memorySub) Close() {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.ps.removeSub(s.roomID, s.id)
	close(s.ch)
}

type MemoryRoomPubSub struct {
	mu   sync.RWMutex
	subs map[string]map[string]chan domain.ChatMessage
}

func NewMemoryRoomPubSub() *MemoryRoomPubSub {
	return &MemoryRoomPubSub{subs: map[string]map[string]chan domain.ChatMessage{}}
}

func (m *MemoryRoomPubSub) Publish(_ context.Context, roomID string, msg domain.ChatMessage) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	subs, ok := m.subs[roomID]
	if !ok {
		return
	}
	for _, ch := range subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (m *MemoryRoomPubSub) Subscribe(ctx context.Context, roomID string, buffer int) Subscription {
	if buffer <= 0 {
		buffer = 64
	}
	subID := id.New()
	ch := make(chan domain.ChatMessage, buffer)

	m.mu.Lock()
	if _, ok := m.subs[roomID]; !ok {
		m.subs[roomID] = map[string]chan domain.ChatMessage{}
	}
	m.subs[roomID][subID] = ch
	m.mu.Unlock()

	sub := &memorySub{id: subID, ch: ch, roomID: roomID, ps: m}
	go func() {
		<-ctx.Done()
		sub.Close()
	}()
	return sub
}

func (m *MemoryRoomPubSub) removeSub(roomID, subID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	subs, ok := m.subs[roomID]
	if !ok {
		return
	}
	delete(subs, subID)
	if len(subs) == 0 {
		delete(m.subs, roomID)
	}
}
