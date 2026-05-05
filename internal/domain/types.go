package domain

import "time"

type RoomType string

const (
	RoomTypeGeneralGroup     RoomType = "GENERAL_GROUP"
	RoomTypeBoardLinkedGroup RoomType = "BOARD_LINKED_GROUP"
)

type MemberRole string

const (
	MemberRoleOwner  MemberRole = "OWNER"
	MemberRoleMember MemberRole = "MEMBER"
)

type MemberStatus string

const (
	MemberStatusActive  MemberStatus = "ACTIVE"
	MemberStatusRemoved MemberStatus = "REMOVED"
	MemberStatusLeft    MemberStatus = "LEFT"
)

type MessageType string

const (
	MessageTypeText   MessageType = "TEXT"
	MessageTypeSystem MessageType = "SYSTEM"
	MessageTypeImage  MessageType = "IMAGE"
	MessageTypeFile   MessageType = "FILE"
)

type ChatRoom struct {
	ID            string
	RoomType      RoomType
	Title         string
	LinkedBoardID string
	OwnerUserID   string
	IsActive      bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     *time.Time
}

type ChatRoomMember struct {
	ID                 string
	RoomID             string
	UserID             string
	Role               MemberRole
	Status             MemberStatus
	JoinedAt           time.Time
	LeftAt             *time.Time
	RemovedAt          *time.Time
	RemovedByUserID    string
	LastReadSequenceNo int64
	LastReadAt         *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type ChatMessage struct {
	ID              string
	RoomID          string
	SenderUserID    string
	MessageType     MessageType
	SequenceNo      int64
	Content         string
	ImageURL        string
	FileURL         string
	Metadata        map[string]any
	IsDeleted       bool
	DeletedAt       *time.Time
	DeletedByUserID string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ChatRoomSummary struct {
	Room        ChatRoom
	LastMessage *ChatMessage
	UnreadCnt   int64
}
