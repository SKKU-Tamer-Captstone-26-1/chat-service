package auth

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func UnaryInterceptor(authenticator Authenticator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		principal, err := authenticateContext(ctx, authenticator)
		if err != nil {
			return nil, mapAuthError(err)
		}
		return handler(WithPrincipal(ctx, principal), req)
	}
}

func StreamInterceptor(authenticator Authenticator) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		principal, err := authenticateContext(stream.Context(), authenticator)
		if err != nil {
			return mapAuthError(err)
		}
		return handler(srv, &principalServerStream{
			ServerStream: stream,
			ctx:          WithPrincipal(stream.Context(), principal),
		})
	}
}

type principalServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *principalServerStream) Context() context.Context {
	return s.ctx
}

func authenticateContext(ctx context.Context, authenticator Authenticator) (Principal, error) {
	if authenticator == nil {
		return Principal{}, ErrUnauthenticated
	}
	token := bearerTokenFromContext(ctx)
	if token == "" {
		return Principal{}, ErrUnauthenticated
	}
	return authenticator.Authenticate(ctx, token)
}

func bearerTokenFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get("authorization")
	for _, value := range values {
		fields := strings.Fields(value)
		if len(fields) == 2 && strings.EqualFold(fields[0], "bearer") {
			return strings.TrimSpace(fields[1])
		}
	}
	return ""
}

func mapAuthError(err error) error {
	if err == nil {
		return nil
	}
	return status.Error(codes.Unauthenticated, err.Error())
}
