package handler

import (
	"context"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"pim/internal/friend/model"
	pbfriend "pim/internal/friend/pb"
	friendservice "pim/internal/friend/service"
)

var _ pbfriend.FriendServiceServer = (*GRPCFriendServer)(nil)

// GRPCFriendServer 实现 friend gRPC 接口。
type GRPCFriendServer struct {
	pbfriend.UnimplementedFriendServiceServer
	svc *friendservice.Service
}

// NewGRPCFriendServer 创建好友 gRPC 处理器。
func NewGRPCFriendServer(svc *friendservice.Service) *GRPCFriendServer {
	return &GRPCFriendServer{svc: svc}
}

// AddFriend 处理添加好友请求。
func (s *GRPCFriendServer) AddFriend(ctx context.Context, req *pbfriend.AddFriendRequest) (*pbfriend.AddFriendResponse, error) {
	traceID := traceIDFromCtx(ctx)
	if req == nil || req.UserId == 0 || req.FriendId == 0 {
		log.Printf("[trace=%s] friend.AddFriend: invalid request", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "invalid request")
	}
	if err := s.svc.AddFriend(uint(req.UserId), uint(req.FriendId)); err != nil {
		log.Printf("[trace=%s] friend.AddFriend: failed to add friend: %v", traceID, err)
		return nil, status.Errorf(codes.Internal, "failed to add friend: %v", err)
	}
	return &pbfriend.AddFriendResponse{Message: "friend added successfully"}, nil
}

// ListFriends 处理好友列表查询请求。
func (s *GRPCFriendServer) ListFriends(ctx context.Context, req *pbfriend.ListFriendsRequest) (*pbfriend.ListFriendsResponse, error) {
	traceID := traceIDFromCtx(ctx)
	if req == nil || req.UserId == 0 {
		log.Printf("[trace=%s] friend.ListFriends: invalid request", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "invalid request")
	}
	friends, err := s.svc.ListFriends(uint(req.UserId))
	if err != nil {
		log.Printf("[trace=%s] friend.ListFriends: failed to list friends: %v", traceID, err)
		return nil, status.Errorf(codes.Internal, "failed to list friends: %v", err)
	}
	pbList := make([]*pbfriend.Friend, 0, len(friends))
	for i := range friends {
		pbList = append(pbList, friendToPB(&friends[i]))
	}
	return &pbfriend.ListFriendsResponse{Friends: pbList}, nil
}

// friendToPB 转换好友领域对象为 protobuf 对象。
func friendToPB(f *model.Friend) *pbfriend.Friend {
	if f == nil {
		return nil
	}
	return &pbfriend.Friend{
		Id:        uint64(f.ID),
		UserId:    uint64(f.UserID),
		FriendId:  uint64(f.FriendID),
		CreatedAt: f.CreatedAt.Unix(),
	}
}

// traceIDFromCtx 提取 trace_id 用于日志关联。
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
