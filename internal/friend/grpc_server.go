package friend

import (
	"context"
	"log"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pbfriend "pim/internal/friend/pb"
)

var _ pbfriend.FriendServiceServer = (*GRPCFriendServer)(nil)

type GRPCFriendServer struct {
	pbfriend.UnimplementedFriendServiceServer
	svc *Service
}

func (s *GRPCFriendServer) AddFriend(ctx context.Context, req *pbfriend.AddFriendRequest) (*pbfriend.AddFriendResponse, error) {
	traceID := traceIDFromCtx(ctx)
	// 验证请求
	if req == nil || req.UserId == 0 || req.FriendId == 0 {
		log.Printf("[trace=%s] friend.AddFriend: invalid request", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "invalid request")
	}
	// 调用服务
	if err := s.svc.AddFriend(uint(req.UserId), uint(req.FriendId)); err != nil {
		log.Printf("[trace=%s] friend.AddFriend: failed to add friend: %v", traceID, err)
		return nil, status.Errorf(codes.Internal, "failed to add friend: %v", err)
	}
	return &pbfriend.AddFriendResponse{Message: "friend added successfully"}, nil
}

func (s *GRPCFriendServer) ListFriends(ctx context.Context, req *pbfriend.ListFriendsRequest) (*pbfriend.ListFriendsResponse, error) {
	traceID := traceIDFromCtx(ctx)
	// 验证请求
	if req == nil || req.UserId == 0 {
		log.Printf("[trace=%s] friend.ListFriends: invalid request", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "invalid request")
	}
	// 调用服务
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

func NewGRPCFriendServer(db *gorm.DB, redisClient *redis.Client) *GRPCFriendServer {
	return &GRPCFriendServer{svc: NewService(db, redisClient)}
}

func friendToPB(f *Friend) *pbfriend.Friend {
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

// traceIDFromCtx 从 gRPC metadata 中提取 x-trace-id。
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
