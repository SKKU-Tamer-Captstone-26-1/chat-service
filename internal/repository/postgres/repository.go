package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ontheblock/chat-service/internal/domain"
	"github.com/ontheblock/chat-service/internal/repository"
)

type Runner interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) asRepo() *txStore { return &txStore{q: s.db} }

func (s *Store) WithTx(ctx context.Context, fn func(ctx context.Context, repo repository.ChatRepository) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	txRepo := &txStore{q: tx}
	if err := fn(ctx, txRepo); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) CreateRoom(ctx context.Context, room domain.ChatRoom) error {
	return s.asRepo().CreateRoom(ctx, room)
}

func (s *Store) GetRoom(ctx context.Context, roomID string) (domain.ChatRoom, error) {
	return s.asRepo().GetRoom(ctx, roomID)
}

func (s *Store) GetActiveBoardLinkedRoom(ctx context.Context, boardID string) (domain.ChatRoom, error) {
	return s.asRepo().GetActiveBoardLinkedRoom(ctx, boardID)
}

func (s *Store) UpdateRoom(ctx context.Context, room domain.ChatRoom) error {
	return s.asRepo().UpdateRoom(ctx, room)
}

func (s *Store) ListRoomsByUser(ctx context.Context, userID string, limit int, pageToken string) ([]domain.ChatRoomSummary, string, error) {
	return s.asRepo().ListRoomsByUser(ctx, userID, limit, pageToken)
}

func (s *Store) GetMember(ctx context.Context, roomID, userID string) (domain.ChatRoomMember, error) {
	return s.asRepo().GetMember(ctx, roomID, userID)
}

func (s *Store) CreateMember(ctx context.Context, member domain.ChatRoomMember) error {
	return s.asRepo().CreateMember(ctx, member)
}

func (s *Store) UpdateMember(ctx context.Context, member domain.ChatRoomMember) error {
	return s.asRepo().UpdateMember(ctx, member)
}

func (s *Store) ListActiveMembersByJoinOrder(ctx context.Context, roomID string) ([]domain.ChatRoomMember, error) {
	return s.asRepo().ListActiveMembersByJoinOrder(ctx, roomID)
}

func (s *Store) CreateMessageWithNextSequence(ctx context.Context, msg domain.ChatMessage) (domain.ChatMessage, error) {
	var saved domain.ChatMessage
	err := s.WithTx(ctx, func(ctx context.Context, repo repository.ChatRepository) error {
		var err error
		saved, err = repo.CreateMessageWithNextSequence(ctx, msg)
		return err
	})
	if err != nil {
		return domain.ChatMessage{}, err
	}
	return saved, nil
}

func (s *Store) GetMessage(ctx context.Context, roomID, messageID string) (domain.ChatMessage, error) {
	return s.asRepo().GetMessage(ctx, roomID, messageID)
}

func (s *Store) UpdateMessage(ctx context.Context, msg domain.ChatMessage) error {
	return s.asRepo().UpdateMessage(ctx, msg)
}

func (s *Store) ListMessagesBefore(ctx context.Context, roomID string, beforeSequence int64, limit int) ([]domain.ChatMessage, int64, error) {
	return s.asRepo().ListMessagesBefore(ctx, roomID, beforeSequence, limit)
}

type txStore struct {
	q Runner
}

func (t *txStore) CreateRoom(ctx context.Context, room domain.ChatRoom) error {
	_, err := t.q.ExecContext(ctx, `
INSERT INTO chat_rooms (id, room_type, title, linked_board_id, owner_user_id, is_active, deleted_at, created_at, updated_at)
VALUES ($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,$7,$8,$9)
`, room.ID, room.RoomType, room.Title, room.LinkedBoardID, room.OwnerUserID, room.IsActive, room.DeletedAt, room.CreatedAt, room.UpdatedAt)
	if isUniqueViolation(err) {
		return domain.ErrAlreadyExists
	}
	return err
}

func (t *txStore) GetRoom(ctx context.Context, roomID string) (domain.ChatRoom, error) {
	var room domain.ChatRoom
	var deletedAt sql.NullTime
	err := t.q.QueryRowContext(ctx, `
SELECT id, room_type, title, COALESCE(linked_board_id::text, ''), owner_user_id, is_active, created_at, updated_at, deleted_at
FROM chat_rooms WHERE id = $1
`, roomID).Scan(&room.ID, &room.RoomType, &room.Title, &room.LinkedBoardID, &room.OwnerUserID, &room.IsActive, &room.CreatedAt, &room.UpdatedAt, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChatRoom{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ChatRoom{}, err
	}
	if deletedAt.Valid {
		t := deletedAt.Time
		room.DeletedAt = &t
	}
	return room, nil
}

func (t *txStore) GetActiveBoardLinkedRoom(ctx context.Context, boardID string) (domain.ChatRoom, error) {
	var room domain.ChatRoom
	var deletedAt sql.NullTime
	err := t.q.QueryRowContext(ctx, `
SELECT id, room_type, title, COALESCE(linked_board_id::text,''), owner_user_id, is_active, created_at, updated_at, deleted_at
FROM chat_rooms
WHERE room_type = 'BOARD_LINKED_GROUP' AND linked_board_id = $1 AND is_active = true AND deleted_at IS NULL
LIMIT 1
`, boardID).Scan(&room.ID, &room.RoomType, &room.Title, &room.LinkedBoardID, &room.OwnerUserID, &room.IsActive, &room.CreatedAt, &room.UpdatedAt, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChatRoom{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ChatRoom{}, err
	}
	if deletedAt.Valid {
		t := deletedAt.Time
		room.DeletedAt = &t
	}
	return room, nil
}

func (t *txStore) UpdateRoom(ctx context.Context, room domain.ChatRoom) error {
	res, err := t.q.ExecContext(ctx, `
UPDATE chat_rooms
SET room_type = $2, title = $3, linked_board_id = NULLIF($4,'')::uuid, owner_user_id = $5,
    is_active = $6, deleted_at = $7, updated_at = $8
WHERE id = $1
`, room.ID, room.RoomType, room.Title, room.LinkedBoardID, room.OwnerUserID, room.IsActive, room.DeletedAt, room.UpdatedAt)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (t *txStore) ListRoomsByUser(ctx context.Context, userID string, limit int, pageToken string) ([]domain.ChatRoomSummary, string, error) {
	if limit <= 0 {
		limit = 20
	}
	lastUpdated, lastRoomID := decodeRoomToken(pageToken)
	args := []any{userID, limit + 1}
	q := `
SELECT r.id, r.room_type, r.title, COALESCE(r.linked_board_id::text,''), r.owner_user_id, r.is_active, r.created_at, r.updated_at, r.deleted_at,
       COALESCE((SELECT COUNT(1) FROM chat_messages m WHERE m.room_id = r.id AND m.sequence_no > COALESCE(mem.last_read_sequence_no,0)), 0)
FROM chat_room_members mem
JOIN chat_rooms r ON r.id = mem.room_id
WHERE mem.user_id = $1 AND mem.status = 'ACTIVE'
`
	if lastUpdated != nil {
		args = append(args, *lastUpdated, lastRoomID)
		q += fmt.Sprintf(" AND (r.updated_at < $%d OR (r.updated_at = $%d AND r.id::text < $%d))\n", len(args)-1, len(args)-1, len(args))
	}
	q += "ORDER BY r.updated_at DESC, r.id DESC LIMIT $2"

	rows, err := t.q.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	out := []domain.ChatRoomSummary{}
	for rows.Next() {
		var r domain.ChatRoom
		var deletedAt sql.NullTime
		var unread int64
		if err := rows.Scan(&r.ID, &r.RoomType, &r.Title, &r.LinkedBoardID, &r.OwnerUserID, &r.IsActive, &r.CreatedAt, &r.UpdatedAt, &deletedAt, &unread); err != nil {
			return nil, "", err
		}
		if deletedAt.Valid {
			t := deletedAt.Time
			r.DeletedAt = &t
		}
		out = append(out, domain.ChatRoomSummary{Room: r, UnreadCnt: unread})
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	nextToken := ""
	if len(out) > limit {
		last := out[limit-1].Room
		nextToken = encodeRoomToken(last.UpdatedAt, last.ID)
		out = out[:limit]
	}
	return out, nextToken, nil
}

func (t *txStore) GetMember(ctx context.Context, roomID, userID string) (domain.ChatRoomMember, error) {
	var m domain.ChatRoomMember
	var leftAt, removedAt, lastReadAt sql.NullTime
	var removedBy sql.NullString
	err := t.q.QueryRowContext(ctx, `
SELECT id, room_id, user_id, role, status, joined_at, left_at, removed_at, removed_by_user_id::text,
       COALESCE(last_read_sequence_no,0), last_read_at, created_at, updated_at
FROM chat_room_members WHERE room_id = $1 AND user_id = $2
`, roomID, userID).Scan(&m.ID, &m.RoomID, &m.UserID, &m.Role, &m.Status, &m.JoinedAt, &leftAt, &removedAt, &removedBy, &m.LastReadSequenceNo, &lastReadAt, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChatRoomMember{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ChatRoomMember{}, err
	}
	if leftAt.Valid {
		t := leftAt.Time
		m.LeftAt = &t
	}
	if removedAt.Valid {
		t := removedAt.Time
		m.RemovedAt = &t
	}
	if removedBy.Valid {
		m.RemovedByUserID = removedBy.String
	}
	if lastReadAt.Valid {
		t := lastReadAt.Time
		m.LastReadAt = &t
	}
	return m, nil
}

func (t *txStore) CreateMember(ctx context.Context, m domain.ChatRoomMember) error {
	_, err := t.q.ExecContext(ctx, `
INSERT INTO chat_room_members
(id, room_id, user_id, role, status, joined_at, left_at, removed_at, removed_by_user_id, last_read_sequence_no, last_read_at, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,'')::uuid,$10,$11,$12,$13)
`, m.ID, m.RoomID, m.UserID, m.Role, m.Status, m.JoinedAt, m.LeftAt, m.RemovedAt, m.RemovedByUserID, m.LastReadSequenceNo, m.LastReadAt, m.CreatedAt, m.UpdatedAt)
	if isUniqueViolation(err) {
		return domain.ErrAlreadyExists
	}
	return err
}

func (t *txStore) UpdateMember(ctx context.Context, m domain.ChatRoomMember) error {
	res, err := t.q.ExecContext(ctx, `
UPDATE chat_room_members
SET role = $3, status = $4, joined_at = $5, left_at = $6, removed_at = $7,
    removed_by_user_id = NULLIF($8,'')::uuid, last_read_sequence_no = $9, last_read_at = $10, updated_at = $11
WHERE room_id = $1 AND user_id = $2
`, m.RoomID, m.UserID, m.Role, m.Status, m.JoinedAt, m.LeftAt, m.RemovedAt, m.RemovedByUserID, m.LastReadSequenceNo, m.LastReadAt, m.UpdatedAt)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (t *txStore) ListActiveMembersByJoinOrder(ctx context.Context, roomID string) ([]domain.ChatRoomMember, error) {
	rows, err := t.q.QueryContext(ctx, `
SELECT id, room_id, user_id, role, status, joined_at, left_at, removed_at, removed_by_user_id::text,
       COALESCE(last_read_sequence_no,0), last_read_at, created_at, updated_at
FROM chat_room_members
WHERE room_id = $1 AND status = 'ACTIVE'
ORDER BY joined_at ASC, id ASC
`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.ChatRoomMember{}
	for rows.Next() {
		var m domain.ChatRoomMember
		var leftAt, removedAt, lastReadAt sql.NullTime
		var removedBy sql.NullString
		if err := rows.Scan(&m.ID, &m.RoomID, &m.UserID, &m.Role, &m.Status, &m.JoinedAt, &leftAt, &removedAt, &removedBy, &m.LastReadSequenceNo, &lastReadAt, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		if leftAt.Valid {
			t := leftAt.Time
			m.LeftAt = &t
		}
		if removedAt.Valid {
			t := removedAt.Time
			m.RemovedAt = &t
		}
		if removedBy.Valid {
			m.RemovedByUserID = removedBy.String
		}
		if lastReadAt.Valid {
			t := lastReadAt.Time
			m.LastReadAt = &t
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (t *txStore) CreateMessageWithNextSequence(ctx context.Context, msg domain.ChatMessage) (domain.ChatMessage, error) {
	metaBytes, err := json.Marshal(msg.Metadata)
	if err != nil {
		return domain.ChatMessage{}, err
	}
	if _, err := t.q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, msg.RoomID); err != nil {
		return domain.ChatMessage{}, err
	}
	query := `
INSERT INTO chat_messages
(id, room_id, sender_user_id, message_type, sequence_no, content, image_url, metadata_json, is_deleted, deleted_at, deleted_by_user_id, created_at, updated_at)
SELECT $1, $2, $3, $4,
       COALESCE((SELECT MAX(sequence_no) + 1 FROM chat_messages WHERE room_id = $2), 1),
       $5, $6, $7::jsonb, false, NULL, NULL, $8, $9
WHERE EXISTS (SELECT 1 FROM chat_rooms WHERE id = $2)
RETURNING sequence_no
`
	if err := t.q.QueryRowContext(ctx, query, msg.ID, msg.RoomID, msg.SenderUserID, msg.MessageType, msg.Content, msg.ImageURL, string(metaBytes), msg.CreatedAt, msg.UpdatedAt).Scan(&msg.SequenceNo); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ChatMessage{}, domain.ErrNotFound
		}
		return domain.ChatMessage{}, err
	}
	return msg, nil
}

func (t *txStore) GetMessage(ctx context.Context, roomID, messageID string) (domain.ChatMessage, error) {
	var m domain.ChatMessage
	var meta []byte
	var deletedAt sql.NullTime
	var deletedBy sql.NullString
	err := t.q.QueryRowContext(ctx, `
SELECT id, room_id, sender_user_id, message_type, sequence_no, COALESCE(content,''), COALESCE(image_url,''), metadata_json,
       is_deleted, deleted_at, deleted_by_user_id::text, created_at, updated_at
FROM chat_messages
WHERE room_id = $1 AND id = $2
`, roomID, messageID).Scan(&m.ID, &m.RoomID, &m.SenderUserID, &m.MessageType, &m.SequenceNo, &m.Content, &m.ImageURL, &meta, &m.IsDeleted, &deletedAt, &deletedBy, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ChatMessage{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ChatMessage{}, err
	}
	if len(meta) > 0 {
		_ = json.Unmarshal(meta, &m.Metadata)
	}
	if deletedAt.Valid {
		t := deletedAt.Time
		m.DeletedAt = &t
	}
	if deletedBy.Valid {
		m.DeletedByUserID = deletedBy.String
	}
	return m, nil
}

func (t *txStore) UpdateMessage(ctx context.Context, m domain.ChatMessage) error {
	metaBytes, err := json.Marshal(m.Metadata)
	if err != nil {
		return err
	}
	res, err := t.q.ExecContext(ctx, `
UPDATE chat_messages
SET content = $3, image_url = $4, metadata_json = $5::jsonb, is_deleted = $6,
    deleted_at = $7, deleted_by_user_id = NULLIF($8,'')::uuid, updated_at = $9
WHERE room_id = $1 AND id = $2
`, m.RoomID, m.ID, m.Content, m.ImageURL, string(metaBytes), m.IsDeleted, m.DeletedAt, m.DeletedByUserID, m.UpdatedAt)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (t *txStore) ListMessagesBefore(ctx context.Context, roomID string, beforeSequence int64, limit int) ([]domain.ChatMessage, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	args := []any{roomID, limit}
	where := ""
	if beforeSequence > 0 {
		where = " AND sequence_no < $3"
		args = append(args, beforeSequence)
	}
	rows, err := t.q.QueryContext(ctx, `
SELECT id, room_id, sender_user_id, message_type, sequence_no, COALESCE(content,''), COALESCE(image_url,''), metadata_json,
       is_deleted, deleted_at, deleted_by_user_id::text, created_at, updated_at
FROM chat_messages
WHERE room_id = $1`+where+`
ORDER BY sequence_no DESC
LIMIT $2
`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []domain.ChatMessage{}
	for rows.Next() {
		var m domain.ChatMessage
		var meta []byte
		var deletedAt sql.NullTime
		var deletedBy sql.NullString
		if err := rows.Scan(&m.ID, &m.RoomID, &m.SenderUserID, &m.MessageType, &m.SequenceNo, &m.Content, &m.ImageURL, &meta, &m.IsDeleted, &deletedAt, &deletedBy, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, 0, err
		}
		if len(meta) > 0 {
			_ = json.Unmarshal(meta, &m.Metadata)
		}
		if deletedAt.Valid {
			t := deletedAt.Time
			m.DeletedAt = &t
		}
		if deletedBy.Valid {
			m.DeletedByUserID = deletedBy.String
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(out) < limit {
		return out, 0, nil
	}
	next := out[len(out)-1].SequenceNo
	return out, next, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(err.Error(), "23505")
}

func encodeRoomToken(updatedAt time.Time, roomID string) string {
	return fmt.Sprintf("%d|%s", updatedAt.UnixNano(), roomID)
}

func decodeRoomToken(token string) (*time.Time, string) {
	if token == "" {
		return nil, ""
	}
	parts := strings.Split(token, "|")
	if len(parts) != 2 {
		return nil, ""
	}
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, ""
	}
	t := time.Unix(0, ns).UTC()
	return &t, parts[1]
}
