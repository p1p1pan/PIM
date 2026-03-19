package handler

import (
	"context"
	"errors"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"pim/internal/conversation/model"
	pbconversation "pim/internal/conversation/pb"
	conversationservice "pim/internal/conversation/service"
)

type GRPCConversationServer struct {
	pbconversation.UnimplementedConversationServiceServer
	svc *conversationservice.Service
}

// NewGRPCConversationServer 创建会话 gRPC 处理器。
func NewGRPCConversationServer(svc *conversationservice.Service) *GRPCConversationServer {
	return &GRPCConversationServer{svc: svc}
}

// ListMessages 处理消息历史查询请求。
func (s *GRPCConversationServer) ListMessages(ctx context.Context, req *pbconversation.ListMessagesRequest) (*pbconversation.ListMessagesResponse, error) {
	traceID := traceIDFromCtx(ctx)
	if req == nil || req.UserId == 0 || req.OtherId == 0 {
		log.Printf("[trace=%s] conversation.ListMessages: missing user_id or other_id", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "user_id and other_id are required")
	}
	messages, err := s.svc.ListMessages(uint(req.UserId), uint(req.OtherId))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list messages: %v", err)
	}
	pbList := make([]*pbconversation.Message, 0, len(messages))
	for i := range messages {
		pbList = append(pbList, messageToPB(&messages[i]))
	}
	return &pbconversation.ListMessagesResponse{Messages: pbList}, nil
}

// SendMessage 处理发消息请求。
func (s *GRPCConversationServer) SendMessage(ctx context.Context, req *pbconversation.SendMessageRequest) (*pbconversation.SendMessageResponse, error) {
	traceID := traceIDFromCtx(ctx)
	if req == nil || req.FromUserId == 0 || req.ToUserId == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "from_user_id and to_user_id are required")
	}
	if req.Content == "" {
		return nil, status.Errorf(codes.InvalidArgument, "content is required")
	}
	m, err := s.svc.SendMessage(uint(req.FromUserId), uint(req.ToUserId), req.Content)
	if err != nil {
		if errors.Is(err, conversationservice.ErrNotFriends) {
			log.Printf("[trace=%s] conversation.SendMessage: not friends", traceID)
			return nil, status.Errorf(codes.PermissionDenied, "%v", err)
		}
		return nil, status.Errorf(codes.Internal, "failed to send message: %v", err)
	}
	return &pbconversation.SendMessageResponse{Message: messageToPB(m)}, nil
}

// ListConversations 处理会话列表查询请求。
func (s *GRPCConversationServer) ListConversations(ctx context.Context, req *pbconversation.ListConversationsRequest) (*pbconversation.ListConversationsResponse, error) {
	if req == nil || req.UserId == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "user_id is required")
	}
	convs, err := s.svc.ListConversations(uint(req.UserId))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list conversations: %v", err)
	}
	res := &pbconversation.ListConversationsResponse{}
	for i := range convs {
		c := &convs[i]
		res.Conversations = append(res.Conversations, &pbconversation.ConversationSummary{
			Id:            uint64(c.ID),
			UserA:         uint64(c.UserA),
			UserB:         uint64(c.UserB),
			LastMessageId: uint64(c.LastMessageID),
			LastSeq:       uint64(c.LastSeq),
			LastMessageAt: c.LastMessageAt.Unix(),
		})
	}
	return res, nil
}

// messageToPB 把领域消息模型转换为 protobuf。
func messageToPB(m *model.Message) *pbconversation.Message {
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

// traceIDFromCtx 从 gRPC metadata 中提取 trace_id。
func traceIDFromCtx(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get("x-trace-id")
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
