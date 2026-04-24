package grpc

import "github.com/ontheblock/chat-service/internal/service"

// Server wiring will be completed after protobuf code generation is added.
// This placeholder keeps transport package boundaries explicit.
type Server struct {
	svc *service.ChatService
}

func NewServer(svc *service.ChatService) *Server {
	return &Server{svc: svc}
}
