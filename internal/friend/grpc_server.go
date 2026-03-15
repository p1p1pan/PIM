package friend

import (
	"context"
	pbfriend "pim/internal/friend/pb"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

var _ pbfriend.FriendServiceServer = (*GRPCFriendServer)(nil)

type GRPCFriendServer struct {
	pbfriend.UnimplementedFriendServiceServer
	svc *Service
}

func (s *GRPCFriendServer) AddFriend(ctx context.Context, req *pbfriend.AddFriendRequest) (*pbfriend.AddFriendResponse, error) {
	// 验证请求
	if req == nil || req.UserId == 0 || req.FriendId == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "invalid request")
	}
	// 调用服务
	if err := s.svc.AddFriend(uint(req.UserId), uint(req.FriendId)); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to add friend: %v", err)
	}
	return &pbfriend.AddFriendResponse{Message: "friend added successfully"}, nil
}

func (s *GRPCFriendServer) ListFriends(ctx context.Context, req *pbfriend.ListFriendsRequest) (*pbfriend.ListFriendsResponse, error) {
	// 验证请求
	if req == nil || req.UserId == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "invalid request")
	}
	// 调用服务
	friends, err := s.svc.ListFriends(uint(req.UserId))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list friends: %v", err)
	}
	pbList := make([]*pbfriend.Friend, 0, len(friends))
	for i := range friends {
		pbList = append(pbList, friendToPB(&friends[i]))
	}
	return &pbfriend.ListFriendsResponse{Friends: pbList}, nil
}

func NewGRPCFriendServer(db *gorm.DB) *GRPCFriendServer {
	return &GRPCFriendServer{svc: NewService(db)}
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
