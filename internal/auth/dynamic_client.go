package auth

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	_ "google.golang.org/protobuf/types/known/timestamppb"
)

const (
	validateTokenMethod = "/ontheblock.auth.v1.AuthService/ValidateToken"
	getMeMethod         = "/ontheblock.auth.v1.AuthService/GetMe"
)

type DynamicAuthServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewDynamicAuthServiceClient(cc grpc.ClientConnInterface) *DynamicAuthServiceClient {
	return &DynamicAuthServiceClient{cc: cc}
}

func (c *DynamicAuthServiceClient) ValidateToken(ctx context.Context, accessToken string) (TokenClaims, error) {
	descs, err := authDescriptors()
	if err != nil {
		return TokenClaims{}, err
	}

	req := dynamicpb.NewMessage(descs.validateTokenRequest)
	req.Set(field(descs.validateTokenRequest, "access_token"), protoreflect.ValueOfString(accessToken))

	resp := dynamicpb.NewMessage(descs.validateTokenResponse)
	if err := c.cc.Invoke(ctx, validateTokenMethod, req, resp); err != nil {
		return TokenClaims{}, err
	}

	return TokenClaims{
		Valid:    boolField(resp, "valid"),
		UserID:   stringField(resp, "user_id"),
		Email:    stringField(resp, "email"),
		Role:     Role(enumField(resp, "role")),
		Reason:   stringField(resp, "reason"),
		Nickname: stringField(resp, "nickname"),
	}, nil
}

func (c *DynamicAuthServiceClient) GetMe(ctx context.Context, userID string) (UserProfile, error) {
	descs, err := authDescriptors()
	if err != nil {
		return UserProfile{}, err
	}

	req := dynamicpb.NewMessage(descs.getMeRequest)
	req.Set(field(descs.getMeRequest, "user_id"), protoreflect.ValueOfString(userID))

	resp := dynamicpb.NewMessage(descs.getMeResponse)
	if err := c.cc.Invoke(ctx, getMeMethod, req, resp); err != nil {
		return UserProfile{}, err
	}

	userValue := resp.Get(field(descs.getMeResponse, "user"))
	if !userValue.IsValid() || userValue.Message() == nil {
		return UserProfile{}, fmt.Errorf("auth GetMe returned empty user")
	}
	user := userValue.Message()
	return UserProfile{
		UserID:          messageStringField(user, "user_id"),
		Email:           messageStringField(user, "email"),
		Nickname:        messageStringField(user, "nickname"),
		ProfileImageURL: messageStringField(user, "profile_image_url"),
		Role:            Role(messageEnumField(user, "role")),
	}, nil
}

type descriptorSet struct {
	validateTokenRequest  protoreflect.MessageDescriptor
	validateTokenResponse protoreflect.MessageDescriptor
	getMeRequest          protoreflect.MessageDescriptor
	getMeResponse         protoreflect.MessageDescriptor
	userResponse          protoreflect.MessageDescriptor
}

var (
	descriptorOnce sync.Once
	descriptors    descriptorSet
	descriptorErr  error
)

func authDescriptors() (descriptorSet, error) {
	descriptorOnce.Do(func() {
		descriptors, descriptorErr = buildAuthDescriptors()
	})
	return descriptors, descriptorErr
}

func buildAuthDescriptors() (descriptorSet, error) {
	fd := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("proto/auth/v1/auth.proto"),
		Package:    proto.String("ontheblock.auth.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/protobuf/timestamp.proto"},
		EnumType: []*descriptorpb.EnumDescriptorProto{{
			Name: proto.String("Role"),
			Value: []*descriptorpb.EnumValueDescriptorProto{
				{Name: proto.String("ROLE_UNSPECIFIED"), Number: proto.Int32(0)},
				{Name: proto.String("ROLE_NORMAL"), Number: proto.Int32(1)},
				{Name: proto.String("ROLE_ADMIN"), Number: proto.Int32(2)},
				{Name: proto.String("ROLE_BAR"), Number: proto.Int32(3)},
				{Name: proto.String("ROLE_REQUE"), Number: proto.Int32(4)},
			},
		}},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("ValidateTokenRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringFieldDescriptor("access_token", 1),
				},
			},
			{
				Name: proto.String("ValidateTokenResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{
					boolFieldDescriptor("valid", 1),
					stringFieldDescriptor("user_id", 2),
					stringFieldDescriptor("email", 3),
					enumFieldDescriptor("role", 4, ".ontheblock.auth.v1.Role"),
					messageFieldDescriptor("expires_at", 5, ".google.protobuf.Timestamp"),
					stringFieldDescriptor("reason", 6),
					stringFieldDescriptor("nickname", 7),
				},
			},
			{
				Name: proto.String("GetMeRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringFieldDescriptor("user_id", 1),
				},
			},
			{
				Name: proto.String("GetMeResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{
					messageFieldDescriptor("user", 1, ".ontheblock.auth.v1.UserResponse"),
				},
			},
			{
				Name: proto.String("UserResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringFieldDescriptor("user_id", 1),
					stringFieldDescriptor("email", 2),
					stringFieldDescriptor("nickname", 3),
					stringFieldDescriptor("profile_image_url", 4),
					enumFieldDescriptor("role", 5, ".ontheblock.auth.v1.Role"),
					messageFieldDescriptor("created_at", 6, ".google.protobuf.Timestamp"),
					stringFieldDescriptor("neighborhood", 7),
					int32FieldDescriptor("alcohol_score", 8),
					int32FieldDescriptor("points", 9),
					boolFieldDescriptor("survey_completed", 10),
					int64FieldDescriptor("survey_id", 11),
				},
			},
		},
	}

	file, err := protodesc.NewFile(fd, protoregistry.GlobalFiles)
	if err != nil {
		return descriptorSet{}, err
	}
	messages := file.Messages()
	return descriptorSet{
		validateTokenRequest:  messages.ByName("ValidateTokenRequest"),
		validateTokenResponse: messages.ByName("ValidateTokenResponse"),
		getMeRequest:          messages.ByName("GetMeRequest"),
		getMeResponse:         messages.ByName("GetMeResponse"),
		userResponse:          messages.ByName("UserResponse"),
	}, nil
}

func field(md protoreflect.MessageDescriptor, name protoreflect.Name) protoreflect.FieldDescriptor {
	return md.Fields().ByName(name)
}

func stringField(msg *dynamicpb.Message, name protoreflect.Name) string {
	return msg.Get(field(msg.Descriptor(), name)).String()
}

func boolField(msg *dynamicpb.Message, name protoreflect.Name) bool {
	return msg.Get(field(msg.Descriptor(), name)).Bool()
}

func enumField(msg *dynamicpb.Message, name protoreflect.Name) int32 {
	return int32(msg.Get(field(msg.Descriptor(), name)).Enum())
}

func messageStringField(msg protoreflect.Message, name protoreflect.Name) string {
	return msg.Get(field(msg.Descriptor(), name)).String()
}

func messageEnumField(msg protoreflect.Message, name protoreflect.Name) int32 {
	return int32(msg.Get(field(msg.Descriptor(), name)).Enum())
}

func stringFieldDescriptor(name string, number int32) *descriptorpb.FieldDescriptorProto {
	return scalarFieldDescriptor(name, number, descriptorpb.FieldDescriptorProto_TYPE_STRING)
}

func boolFieldDescriptor(name string, number int32) *descriptorpb.FieldDescriptorProto {
	return scalarFieldDescriptor(name, number, descriptorpb.FieldDescriptorProto_TYPE_BOOL)
}

func int32FieldDescriptor(name string, number int32) *descriptorpb.FieldDescriptorProto {
	return scalarFieldDescriptor(name, number, descriptorpb.FieldDescriptorProto_TYPE_INT32)
}

func int64FieldDescriptor(name string, number int32) *descriptorpb.FieldDescriptorProto {
	return scalarFieldDescriptor(name, number, descriptorpb.FieldDescriptorProto_TYPE_INT64)
}

func scalarFieldDescriptor(name string, number int32, typ descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:   proto.String(name),
		Number: proto.Int32(number),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:   typ.Enum(),
	}
}

func enumFieldDescriptor(name string, number int32, typeName string) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(number),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
		TypeName: proto.String(typeName),
	}
}

func messageFieldDescriptor(name string, number int32, typeName string) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(number),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: proto.String(typeName),
	}
}
