package conversation	

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
	pbconversation "pim/internal/conversation/pb"
)
var _ pbconversation.ConversationServiceServer = (*GRPCConversationServer)(nil)
// GRPCConversationServer 实现 ConversationServiceServer 接口
type GRPCConversationServer struct {
	pbconversation.UnimplementedConversationServiceServer
	svc *Service
}

func (s *GRPCConversationServer) ListMessages(ctx context.Context, req *pbconversation.ListMessagesRequest) (*pbconversation.ListMessagesResponse, error) {
	// 验证请求
	if req == nil || req.UserId == 0 || req.OtherId == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "user_id and other_id are required")
	}
	// 调用服务
	messages, err := s.svc.ListMessages(uint(req.UserId), uint(req.OtherId))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list messages: %v", err)
	}
	// 转换为 pb 消息列表
	pbList := make([]*pbconversation.Message, 0, len(messages))
	for i := range messages {
		pbList = append(pbList, messageToPB(&messages[i]))
	}
	// 返回响应
	return &pbconversation.ListMessagesResponse{Messages: pbList}, nil
}

func (s *GRPCConversationServer) SendMessage(ctx context.Context, req *pbconversation.SendMessageRequest) (*pbconversation.SendMessageResponse, error) {
	// 验证请求
	if req == nil || req.FromUserId == 0 || req.ToUserId == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "from_user_id and to_user_id are required")
	}
	// 验证内容
	if req.Content == "" {
		return nil, status.Errorf(codes.InvalidArgument, "content is required")
	}
	// 调用服务
	m, err := s.svc.SendMessage(uint(req.FromUserId), uint(req.ToUserId), req.Content)
	if err != nil {
		// 处理错误
		if errors.Is(err, errors.New("not friends, cannot send message")) {
			return nil, status.Errorf(codes.PermissionDenied, "%v", err)
		}
		return nil, status.Errorf(codes.Internal, "failed to send message: %v", err)
	}
	// 转换为 pb 消息
	return &pbconversation.SendMessageResponse{Message: messageToPB(m)}, nil
}
// NewGRPCConversationServer 用 db 构造 gRPC 服务端，供 cmd/conversation-service 注册。
func NewGRPCConversationServer(db *gorm.DB) *GRPCConversationServer {
	return &GRPCConversationServer{svc: NewService(db)}
}
func messageToPB(m *Message) *pbconversation.Message {
	if m == nil {
		return nil
	}
	return &pbconversation.Message{
		Id:         uint64(m.ID),
		FromUserId: uint64(m.FromUserID),
		ToUserId:   uint64(m.ToUserID),
		Content:    m.Content,
		CreatedAt:  m.CreatedAt.Unix(),
	}
}