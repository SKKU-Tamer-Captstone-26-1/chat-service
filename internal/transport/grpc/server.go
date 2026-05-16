package grpc

import (
	"context"
	"errors"
	"strings"

	"github.com/ontheblock/chat-service/internal/auth"
	"github.com/ontheblock/chat-service/internal/domain"
	"github.com/ontheblock/chat-service/internal/service"
	chatv1 "github.com/ontheblock/chat-service/proto/chat/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Server struct {
	chatv1.UnimplementedChatServiceServer
	svc *service.ChatService
}

func NewServer(svc *service.ChatService) *Server {
	return &Server{svc: svc}
}

func (s *Server) CreateRoom(ctx context.Context, req *chatv1.CreateRoomRequest) (*chatv1.CreateRoomResponse, error) {
	creatorUserID, err := requestUserID(ctx, req.GetCreatorUserId(), "creator_user_id")
	if err != nil {
		return nil, err
	}
	room, err := s.svc.CreateRoom(ctx, service.CreateRoomInput{CreatorUserID: creatorUserID, Title: req.GetTitle()})
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.CreateRoomResponse{Room: toPBRoom(room)}, nil
}

func (s *Server) CreateBoardLinkedRoom(ctx context.Context, req *chatv1.CreateBoardLinkedRoomRequest) (*chatv1.CreateBoardLinkedRoomResponse, error) {
	creatorUserID, err := requestUserID(ctx, req.GetCreatorUserId(), "creator_user_id")
	if err != nil {
		return nil, err
	}
	if req.GetBoardId() == "" {
		return nil, status.Error(codes.InvalidArgument, "board_id is required")
	}
	room, exists, err := s.svc.CreateBoardLinkedRoom(ctx, service.CreateBoardLinkedRoomInput{
		CreatorUserID: creatorUserID,
		BoardID:       req.GetBoardId(),
		Title:         req.GetTitle(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	resp := &chatv1.CreateBoardLinkedRoomResponse{AlreadyExists: exists}
	if !exists {
		resp.Room = toPBRoom(room)
	}
	return resp, nil
}

func (s *Server) GetOrCreateBoardChatRoom(ctx context.Context, req *chatv1.GetOrCreateBoardChatRoomRequest) (*chatv1.GetOrCreateBoardChatRoomResponse, error) {
	userID, err := authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetBoardId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "board_id is required")
	}
	entry, err := s.svc.GetOrCreateBoardChatRoom(ctx, service.BoardChatRoomEntryInput{
		UserID:           userID,
		BoardID:          req.GetBoardId(),
		BoardOwnerUserID: req.GetBoardOwnerUserId(),
		Title:            req.GetTitle(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.GetOrCreateBoardChatRoomResponse{
		Room:          toPBRoom(entry.Room),
		Summary:       toPBRoomSummaryFromRoom(entry.Room),
		Member:        toPBMember(entry.Member),
		AlreadyExists: entry.AlreadyExists,
	}, nil
}

func (s *Server) JoinRoom(ctx context.Context, req *chatv1.JoinRoomRequest) (*chatv1.JoinRoomResponse, error) {
	userID, err := requestUserID(ctx, req.GetUserId(), "user_id")
	if err != nil {
		return nil, err
	}
	if req.GetRoomId() == "" {
		return nil, status.Error(codes.InvalidArgument, "room_id is required")
	}
	member, err := s.svc.JoinRoom(ctx, req.GetRoomId(), userID)
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.JoinRoomResponse{Member: toPBMember(member)}, nil
}

func (s *Server) LeaveRoom(ctx context.Context, req *chatv1.LeaveRoomRequest) (*chatv1.LeaveRoomResponse, error) {
	userID, err := requestUserID(ctx, req.GetUserId(), "user_id")
	if err != nil {
		return nil, err
	}
	if req.GetRoomId() == "" {
		return nil, status.Error(codes.InvalidArgument, "room_id is required")
	}
	member, room, err := s.svc.LeaveRoom(ctx, req.GetRoomId(), userID)
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.LeaveRoomResponse{Member: toPBMember(member), Room: toPBRoom(room)}, nil
}

func (s *Server) ListMyRooms(ctx context.Context, req *chatv1.ListMyRoomsRequest) (*chatv1.ListMyRoomsResponse, error) {
	userID, err := requestUserID(ctx, req.GetUserId(), "user_id")
	if err != nil {
		return nil, err
	}
	limit := 20
	pageToken := ""
	if req.GetPagination() != nil {
		if req.GetPagination().GetPageSize() > 0 {
			limit = int(req.GetPagination().GetPageSize())
		}
		pageToken = req.GetPagination().GetPageToken()
	}

	rows, nextToken, err := s.svc.ListMyRooms(ctx, userID, limit, pageToken)
	if err != nil {
		return nil, mapError(err)
	}
	resp := &chatv1.ListMyRoomsResponse{Pagination: &chatv1.PaginationResponse{NextPageToken: nextToken}}
	for _, r := range rows {
		var lastMessage *chatv1.LastMessagePreview
		if r.LastMessage != nil {
			contentPreview := r.LastMessage.Content
			if r.LastMessage.IsDeleted {
				contentPreview = ""
			} else if r.LastMessage.MessageType == domain.MessageTypeImage {
				contentPreview = "[Image]"
			} else if r.LastMessage.MessageType == domain.MessageTypeFile {
				if fileName := extractMessageMetadataString(r.LastMessage.Metadata, "file_name"); fileName != "" {
					contentPreview = "[File] " + fileName
				} else {
					contentPreview = "[File]"
				}
			}
			lastMessage = &chatv1.LastMessagePreview{
				MessageId:      r.LastMessage.ID,
				MessageType:    toPBMessageType(r.LastMessage.MessageType),
				ContentPreview: contentPreview,
				SenderUserId:   r.LastMessage.SenderUserID,
				SequenceNo:     r.LastMessage.SequenceNo,
				SentAt:         timestamppb.New(r.LastMessage.CreatedAt),
			}
		}
		resp.Rooms = append(resp.Rooms, &chatv1.ChatRoomSummary{
			RoomId:        r.Room.ID,
			RoomType:      toPBRoomType(r.Room.RoomType),
			Title:         r.Room.Title,
			LinkedBoardId: r.Room.LinkedBoardID,
			OwnerUserId:   r.Room.OwnerUserID,
			LastMessage:   lastMessage,
			UnreadCount:   r.UnreadCnt,
			UpdatedAt:     timestamppb.New(r.Room.UpdatedAt),
		})
	}
	return resp, nil
}

func (s *Server) GetMessages(ctx context.Context, req *chatv1.GetMessagesRequest) (*chatv1.GetMessagesResponse, error) {
	userID, err := requestUserID(ctx, req.GetUserId(), "user_id")
	if err != nil {
		return nil, err
	}
	if req.GetRoomId() == "" {
		return nil, status.Error(codes.InvalidArgument, "room_id is required")
	}
	msgs, next, err := s.svc.GetMessages(ctx, req.GetRoomId(), userID, req.GetBeforeSequenceNo(), int(req.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}
	resp := &chatv1.GetMessagesResponse{NextBeforeSequenceNo: next}
	for _, m := range msgs {
		resp.Messages = append(resp.Messages, toPBMessage(m))
	}
	return resp, nil
}

func (s *Server) SendMessage(ctx context.Context, req *chatv1.SendMessageRequest) (*chatv1.SendMessageResponse, error) {
	senderUserID, err := requestUserID(ctx, req.GetSenderUserId(), "sender_user_id")
	if err != nil {
		return nil, err
	}
	if req.GetRoomId() == "" {
		return nil, status.Error(codes.InvalidArgument, "room_id is required")
	}
	metadata := map[string]any{}
	if req.GetMetadata() != nil {
		metadata = req.GetMetadata().AsMap()
	}
	if req.GetFileUrl() != "" {
		metadata["file_url"] = req.GetFileUrl()
	}
	msg, err := s.svc.SendMessage(ctx, req.GetRoomId(), senderUserID, fromPBMessageType(req.GetMessageType()), req.GetContent(), req.GetImageUrl(), metadata)
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.SendMessageResponse{Message: toPBMessage(msg)}, nil
}

func (s *Server) CreateAttachmentUploadURL(ctx context.Context, req *chatv1.CreateAttachmentUploadURLRequest) (*chatv1.CreateAttachmentUploadURLResponse, error) {
	userID, err := requestUserID(ctx, req.GetUserId(), "user_id")
	if err != nil {
		return nil, err
	}
	if req.GetRoomId() == "" || req.GetFileName() == "" || req.GetContentType() == "" {
		return nil, status.Error(codes.InvalidArgument, "room_id, file_name, content_type are required")
	}
	out, err := s.svc.CreateAttachmentUploadURL(ctx, req.GetRoomId(), userID, req.GetFileName(), req.GetContentType())
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.CreateAttachmentUploadURLResponse{
		ObjectName: out.ObjectName,
		UploadUrl:  out.UploadURL,
		FileUrl:    out.FileURL,
		ExpiresAt:  timestamppb.New(out.ExpiresAt),
	}, nil
}

func (s *Server) CreateImageUploadURL(ctx context.Context, req *chatv1.CreateImageUploadURLRequest) (*chatv1.CreateImageUploadURLResponse, error) {
	userID, err := requestUserID(ctx, req.GetUserId(), "user_id")
	if err != nil {
		return nil, err
	}
	if req.GetRoomId() == "" || req.GetFileName() == "" || req.GetContentType() == "" {
		return nil, status.Error(codes.InvalidArgument, "room_id, file_name, content_type are required")
	}
	out, err := s.svc.CreateImageUploadURL(ctx, req.GetRoomId(), userID, req.GetFileName(), req.GetContentType())
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.CreateImageUploadURLResponse{
		ObjectName: out.ObjectName,
		UploadUrl:  out.UploadURL,
		ImageUrl:   out.ImageURL,
		ExpiresAt:  timestamppb.New(out.ExpiresAt),
	}, nil
}

func (s *Server) MarkAsRead(ctx context.Context, req *chatv1.MarkAsReadRequest) (*chatv1.MarkAsReadResponse, error) {
	userID, err := requestUserID(ctx, req.GetUserId(), "user_id")
	if err != nil {
		return nil, err
	}
	if req.GetRoomId() == "" {
		return nil, status.Error(codes.InvalidArgument, "room_id is required")
	}
	m, err := s.svc.MarkAsRead(ctx, req.GetRoomId(), userID, req.GetLastReadSequenceNo())
	if err != nil {
		return nil, mapError(err)
	}
	out := &chatv1.MarkAsReadResponse{
		RoomId:             m.RoomID,
		UserId:             m.UserID,
		LastReadSequenceNo: m.LastReadSequenceNo,
	}
	if m.UpdatedAt.Unix() > 0 {
		out.UpdatedAt = timestamppb.New(m.UpdatedAt)
	}
	return out, nil
}

func (s *Server) MarkChatRoomRead(ctx context.Context, req *chatv1.MarkChatRoomReadRequest) (*chatv1.MarkChatRoomReadResponse, error) {
	userID, err := authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}
	if req.GetRoomId() == "" {
		return nil, status.Error(codes.InvalidArgument, "room_id is required")
	}
	m, err := s.svc.MarkChatRoomRead(ctx, req.GetRoomId(), userID)
	if err != nil {
		return nil, mapError(err)
	}
	out := &chatv1.MarkChatRoomReadResponse{
		RoomId:             m.RoomID,
		UserId:             m.UserID,
		LastReadSequenceNo: m.LastReadSequenceNo,
	}
	if m.UpdatedAt.Unix() > 0 {
		out.UpdatedAt = timestamppb.New(m.UpdatedAt)
	}
	return out, nil
}

func (s *Server) RegisterDeviceToken(ctx context.Context, req *chatv1.RegisterDeviceTokenRequest) (*chatv1.RegisterDeviceTokenResponse, error) {
	userID, err := authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}
	token, err := s.svc.RegisterDeviceToken(ctx, userID, req.GetDeviceId(), req.GetToken(), fromPBDevicePlatform(req.GetPlatform()))
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.RegisterDeviceTokenResponse{DeviceToken: toPBDeviceToken(token)}, nil
}

func (s *Server) UnregisterDeviceToken(ctx context.Context, req *chatv1.UnregisterDeviceTokenRequest) (*chatv1.UnregisterDeviceTokenResponse, error) {
	userID, err := authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.svc.UnregisterDeviceToken(ctx, userID, req.GetDeviceId()); err != nil {
		return nil, mapError(err)
	}
	return &chatv1.UnregisterDeviceTokenResponse{}, nil
}

func (s *Server) RemoveMember(ctx context.Context, req *chatv1.RemoveMemberRequest) (*chatv1.RemoveMemberResponse, error) {
	ownerUserID, err := requestUserID(ctx, req.GetOwnerUserId(), "owner_user_id")
	if err != nil {
		return nil, err
	}
	if req.GetRoomId() == "" || req.GetTargetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "room_id and target_user_id are required")
	}
	m, err := s.svc.RemoveMember(ctx, req.GetRoomId(), ownerUserID, req.GetTargetUserId())
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.RemoveMemberResponse{Member: toPBMember(m)}, nil
}

func (s *Server) DeleteMessage(ctx context.Context, req *chatv1.DeleteMessageRequest) (*chatv1.DeleteMessageResponse, error) {
	ownerUserID, err := requestUserID(ctx, req.GetOwnerUserId(), "owner_user_id")
	if err != nil {
		return nil, err
	}
	if req.GetRoomId() == "" || req.GetMessageId() == "" {
		return nil, status.Error(codes.InvalidArgument, "room_id and message_id are required")
	}
	m, err := s.svc.DeleteMessage(ctx, req.GetRoomId(), req.GetMessageId(), ownerUserID)
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.DeleteMessageResponse{Message: toPBMessage(m)}, nil
}

func (s *Server) DeactivateRoom(ctx context.Context, req *chatv1.DeactivateRoomRequest) (*chatv1.DeactivateRoomResponse, error) {
	ownerUserID, err := requestUserID(ctx, req.GetOwnerUserId(), "owner_user_id")
	if err != nil {
		return nil, err
	}
	if req.GetRoomId() == "" {
		return nil, status.Error(codes.InvalidArgument, "room_id is required")
	}
	r, err := s.svc.DeactivateRoom(ctx, req.GetRoomId(), ownerUserID)
	if err != nil {
		return nil, mapError(err)
	}
	return &chatv1.DeactivateRoomResponse{Room: toPBRoom(r)}, nil
}

func (s *Server) StreamMessages(req *chatv1.StreamMessagesRequest, stream chatv1.ChatService_StreamMessagesServer) error {
	userID, err := requestUserID(stream.Context(), req.GetUserId(), "user_id")
	if err != nil {
		return err
	}
	if req.GetRoomId() == "" {
		return status.Error(codes.InvalidArgument, "room_id is required")
	}
	msgCh, errCh := s.svc.StreamMessages(stream.Context(), req.GetRoomId(), userID, req.GetAfterSequenceNo())
	for {
		select {
		case <-stream.Context().Done():
			return nil
		case err, ok := <-errCh:
			if !ok {
				return nil
			}
			if err == nil {
				continue
			}
			if errors.Is(err, service.ErrMemberLeft) {
				return status.Error(codes.FailedPrecondition, err.Error())
			}
			if errors.Is(err, service.ErrMemberRemoved) {
				return status.Error(codes.PermissionDenied, err.Error())
			}
			return mapError(err)
		case msg, ok := <-msgCh:
			if !ok {
				return nil
			}
			if err := stream.Send(&chatv1.StreamMessagesResponse{Message: toPBMessage(msg)}); err != nil {
				return status.Errorf(codes.Unavailable, "stream send failed: %v", err)
			}
		}
	}
}

func requestUserID(ctx context.Context, bodyUserID, fieldName string) (string, error) {
	trimmedBodyUserID := strings.TrimSpace(bodyUserID)
	if principal, ok := auth.PrincipalFromContext(ctx); ok {
		if trimmedBodyUserID != "" && trimmedBodyUserID != principal.UserID {
			return "", status.Errorf(codes.PermissionDenied, "%s does not match authenticated user", fieldName)
		}
		return principal.UserID, nil
	}
	if trimmedBodyUserID == "" {
		return "", status.Errorf(codes.InvalidArgument, "%s is required", fieldName)
	}
	return trimmedBodyUserID, nil
}

func authenticatedUserID(ctx context.Context) (string, error) {
	principal, ok := auth.PrincipalFromContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "authenticated user is required")
	}
	return principal.UserID, nil
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, domain.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, domain.ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrNotConfigured):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, domain.ErrRoomInactive):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, domain.ErrPermissionDenied):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, domain.ErrRemovedCannotRejoin):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, domain.ErrInvalidState):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, service.ErrMemberLeft):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, service.ErrMemberRemoved):
		return status.Error(codes.PermissionDenied, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func toPBRoom(r domain.ChatRoom) *chatv1.ChatRoom {
	out := &chatv1.ChatRoom{
		RoomId:        r.ID,
		RoomType:      toPBRoomType(r.RoomType),
		Title:         r.Title,
		LinkedBoardId: r.LinkedBoardID,
		OwnerUserId:   r.OwnerUserID,
		IsActive:      r.IsActive,
		CreatedAt:     timestamppb.New(r.CreatedAt),
		UpdatedAt:     timestamppb.New(r.UpdatedAt),
	}
	if r.DeletedAt != nil {
		out.DeletedAt = timestamppb.New(*r.DeletedAt)
	}
	return out
}

func toPBRoomSummaryFromRoom(r domain.ChatRoom) *chatv1.ChatRoomSummary {
	return &chatv1.ChatRoomSummary{
		RoomId:        r.ID,
		RoomType:      toPBRoomType(r.RoomType),
		Title:         r.Title,
		LinkedBoardId: r.LinkedBoardID,
		OwnerUserId:   r.OwnerUserID,
		UpdatedAt:     timestamppb.New(r.UpdatedAt),
	}
}

func toPBMember(m domain.ChatRoomMember) *chatv1.ChatRoomMember {
	out := &chatv1.ChatRoomMember{
		MemberId:           m.ID,
		RoomId:             m.RoomID,
		UserId:             m.UserID,
		Role:               toPBMemberRole(m.Role),
		Status:             toPBMemberStatus(m.Status),
		LastReadSequenceNo: m.LastReadSequenceNo,
		JoinedAt:           timestamppb.New(m.JoinedAt),
	}
	if m.LeftAt != nil {
		out.LeftAt = timestamppb.New(*m.LeftAt)
	}
	if m.RemovedAt != nil {
		out.RemovedAt = timestamppb.New(*m.RemovedAt)
	}
	out.RemovedByUserId = m.RemovedByUserID
	return out
}

func toPBMessage(m domain.ChatMessage) *chatv1.ChatMessage {
	out := &chatv1.ChatMessage{
		MessageId:       m.ID,
		RoomId:          m.RoomID,
		SenderUserId:    m.SenderUserID,
		MessageType:     toPBMessageType(m.MessageType),
		SequenceNo:      m.SequenceNo,
		Content:         m.Content,
		ImageUrl:        m.ImageURL,
		FileUrl:         m.FileURL,
		IsDeleted:       m.IsDeleted,
		DeletedByUserId: m.DeletedByUserID,
		SentAt:          timestamppb.New(m.CreatedAt),
		UpdatedAt:       timestamppb.New(m.UpdatedAt),
	}
	if m.Metadata != nil {
		meta, err := structpb.NewStruct(m.Metadata)
		if err == nil {
			out.Metadata = meta
		}
	}
	if m.DeletedAt != nil {
		out.DeletedAt = timestamppb.New(*m.DeletedAt)
	}
	return out
}

func toPBDeviceToken(token domain.DeviceToken) *chatv1.DeviceToken {
	out := &chatv1.DeviceToken{
		UserId:     token.UserID,
		DeviceId:   token.DeviceID,
		Token:      token.Token,
		Platform:   toPBDevicePlatform(token.Platform),
		CreatedAt:  timestamppb.New(token.CreatedAt),
		UpdatedAt:  timestamppb.New(token.UpdatedAt),
		LastSeenAt: timestamppb.New(token.LastSeenAt),
	}
	return out
}

func toPBRoomType(rt domain.RoomType) chatv1.RoomType {
	switch rt {
	case domain.RoomTypeGeneralGroup:
		return chatv1.RoomType_ROOM_TYPE_GENERAL_GROUP
	case domain.RoomTypeBoardLinkedGroup:
		return chatv1.RoomType_ROOM_TYPE_BOARD_LINKED_GROUP
	default:
		return chatv1.RoomType_ROOM_TYPE_UNSPECIFIED
	}
}

func toPBMemberRole(r domain.MemberRole) chatv1.MemberRole {
	switch r {
	case domain.MemberRoleOwner:
		return chatv1.MemberRole_MEMBER_ROLE_OWNER
	case domain.MemberRoleMember:
		return chatv1.MemberRole_MEMBER_ROLE_MEMBER
	default:
		return chatv1.MemberRole_MEMBER_ROLE_UNSPECIFIED
	}
}

func toPBMemberStatus(s domain.MemberStatus) chatv1.MemberStatus {
	switch s {
	case domain.MemberStatusActive:
		return chatv1.MemberStatus_MEMBER_STATUS_ACTIVE
	case domain.MemberStatusLeft:
		return chatv1.MemberStatus_MEMBER_STATUS_LEFT
	case domain.MemberStatusRemoved:
		return chatv1.MemberStatus_MEMBER_STATUS_REMOVED
	default:
		return chatv1.MemberStatus_MEMBER_STATUS_UNSPECIFIED
	}
}

func toPBMessageType(t domain.MessageType) chatv1.MessageType {
	switch t {
	case domain.MessageTypeText:
		return chatv1.MessageType_MESSAGE_TYPE_TEXT
	case domain.MessageTypeSystem:
		return chatv1.MessageType_MESSAGE_TYPE_SYSTEM
	case domain.MessageTypeImage:
		return chatv1.MessageType_MESSAGE_TYPE_IMAGE
	case domain.MessageTypeFile:
		return chatv1.MessageType_MESSAGE_TYPE_FILE
	default:
		return chatv1.MessageType_MESSAGE_TYPE_UNSPECIFIED
	}
}

func fromPBMessageType(t chatv1.MessageType) domain.MessageType {
	switch t {
	case chatv1.MessageType_MESSAGE_TYPE_TEXT:
		return domain.MessageTypeText
	case chatv1.MessageType_MESSAGE_TYPE_SYSTEM:
		return domain.MessageTypeSystem
	case chatv1.MessageType_MESSAGE_TYPE_IMAGE:
		return domain.MessageTypeImage
	case chatv1.MessageType_MESSAGE_TYPE_FILE:
		return domain.MessageTypeFile
	default:
		return domain.MessageTypeText
	}
}

func toPBDevicePlatform(platform domain.DevicePlatform) chatv1.DevicePlatform {
	switch platform {
	case domain.DevicePlatformIOS:
		return chatv1.DevicePlatform_DEVICE_PLATFORM_IOS
	case domain.DevicePlatformAndroid:
		return chatv1.DevicePlatform_DEVICE_PLATFORM_ANDROID
	default:
		return chatv1.DevicePlatform_DEVICE_PLATFORM_UNSPECIFIED
	}
}

func fromPBDevicePlatform(platform chatv1.DevicePlatform) domain.DevicePlatform {
	switch platform {
	case chatv1.DevicePlatform_DEVICE_PLATFORM_IOS:
		return domain.DevicePlatformIOS
	case chatv1.DevicePlatform_DEVICE_PLATFORM_ANDROID:
		return domain.DevicePlatformAndroid
	default:
		return ""
	}
}

func extractMessageMetadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	v, ok := metadata[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
