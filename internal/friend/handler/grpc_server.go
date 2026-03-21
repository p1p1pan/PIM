package handler

import (
	"context"
	"errors"
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

// ListOutgoingFriendRequests 处理“我发出的申请”查询请求。
func (s *GRPCFriendServer) ListOutgoingFriendRequests(ctx context.Context, req *pbfriend.ListOutgoingFriendRequestsRequest) (*pbfriend.ListOutgoingFriendRequestsResponse, error) {
	traceID := traceIDFromCtx(ctx)
	// 非法请求
	if req == nil || req.UserId == 0 {
		log.Printf("[trace=%s] friend.ListOutgoingFriendRequests: invalid request", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "invalid request")
	}
	// 查询“我发出的”好友申请
	rows, nextCursor, hasMore, err := s.svc.ListOutgoingFriendRequests(
		uint(req.UserId),
		req.GetStatus(),
		uint(req.GetCursor()),
		int(req.GetLimit()),
	)
	if err != nil {
		// 非法请求
		if err == friendservice.ErrInvalidStatus || err == friendservice.ErrInvalidUserID {
			log.Printf("[trace=%s] friend.ListOutgoingFriendRequests: bad request: %v", traceID, err)
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		log.Printf("[trace=%s] friend.ListOutgoingFriendRequests: failed: %v", traceID, err)
		return nil, status.Errorf(codes.Internal, "list outgoing friend requests failed: %v", err)
	}
	// 转换为 protobuf 对象
	items := make([]*pbfriend.FriendRequestItem, 0, len(rows))
	for i := range rows {
		items = append(items, friendRequestToPB(&rows[i]))
	}
	return &pbfriend.ListOutgoingFriendRequestsResponse{
		Items:      items,
		NextCursor: uint64(nextCursor),
		HasMore:    hasMore,
	}, nil
}

// ListIncomingFriendRequests 处理“我收到的申请”查询请求。
func (s *GRPCFriendServer) ListIncomingFriendRequests(ctx context.Context, req *pbfriend.ListIncomingFriendRequestsRequest) (*pbfriend.ListIncomingFriendRequestsResponse, error) {
	traceID := traceIDFromCtx(ctx)
	// 非法请求
	if req == nil || req.UserId == 0 {
		log.Printf("[trace=%s] friend.ListIncomingFriendRequests: invalid request", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "invalid request")
	}
	// 查询“我收到的”好友申请
	rows, nextCursor, hasMore, err := s.svc.ListIncomingFriendRequests(
		uint(req.UserId),
		req.GetStatus(),
		uint(req.GetCursor()),
		int(req.GetLimit()),
	)
	if err != nil {
		// 非法请求
		if err == friendservice.ErrInvalidStatus || err == friendservice.ErrInvalidUserID {
			log.Printf("[trace=%s] friend.ListIncomingFriendRequests: bad request: %v", traceID, err)
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		log.Printf("[trace=%s] friend.ListIncomingFriendRequests: failed: %v", traceID, err)
		return nil, status.Errorf(codes.Internal, "list incoming friend requests failed: %v", err)
	}

	// 转换为 protobuf 对象
	items := make([]*pbfriend.FriendRequestItem, 0, len(rows))
	for i := range rows {
		items = append(items, friendRequestToPB(&rows[i]))
	}
	return &pbfriend.ListIncomingFriendRequestsResponse{
		Items:      items,
		NextCursor: uint64(nextCursor),
		HasMore:    hasMore,
	}, nil
}

// SendFriendRequest 处理发送好友申请请求。
func (s *GRPCFriendServer) SendFriendRequest(ctx context.Context, req *pbfriend.SendFriendRequestRequest) (*pbfriend.SendFriendRequestResponse, error) {
	traceID := traceIDFromCtx(ctx)
	if req == nil || req.FromUserId == 0 || req.ToUserId == 0 {
		log.Printf("[trace=%s] friend.SendFriendRequest: invalid request", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "invalid request")
	}

	fr, err := s.svc.SendFriendRequest(uint(req.FromUserId), uint(req.ToUserId), req.Remark)
	if err != nil {
		log.Printf("[trace=%s] friend.SendFriendRequest: failed: %v", traceID, err)
		switch {
		case errors.Is(err, friendservice.ErrInvalidUserID), errors.Is(err, friendservice.ErrCannotAddSelf):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, friendservice.ErrAlreadyFriends), errors.Is(err, friendservice.ErrBlocked):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "send friend request failed: %v", err)
	}

	return &pbfriend.SendFriendRequestResponse{
		RequestId: uint64(fr.ID),
		Status:    fr.Status,
	}, nil
}

// ApproveFriendRequest 处理同意好友申请请求。
func (s *GRPCFriendServer) ApproveFriendRequest(ctx context.Context, req *pbfriend.ApproveFriendRequestRequest) (*pbfriend.ApproveFriendRequestResponse, error) {
	traceID := traceIDFromCtx(ctx)
	// 非法请求
	if req == nil || req.RequestId == 0 || req.OperatorUserId == 0 {
		log.Printf("[trace=%s] friend.ApproveFriendRequest: invalid request", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "invalid request")
	}
	// 同意好友申请
	if err := s.svc.ApproveFriendRequest(uint(req.RequestId), uint(req.OperatorUserId)); err != nil {
		log.Printf("[trace=%s] friend.ApproveFriendRequest: failed: %v", traceID, err)
		switch {
		case errors.Is(err, friendservice.ErrRequestNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		case errors.Is(err, friendservice.ErrRequestStateInvalid), errors.Is(err, friendservice.ErrBlocked):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "approve friend request failed: %v", err)
	}

	return &pbfriend.ApproveFriendRequestResponse{Message: "approved"}, nil
}

// RejectFriendRequest 处理拒绝好友申请请求。
func (s *GRPCFriendServer) RejectFriendRequest(ctx context.Context, req *pbfriend.RejectFriendRequestRequest) (*pbfriend.RejectFriendRequestResponse, error) {
	traceID := traceIDFromCtx(ctx)
	// 非法请求
	if req == nil || req.RequestId == 0 || req.OperatorUserId == 0 {
		log.Printf("[trace=%s] friend.RejectFriendRequest: invalid request", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "invalid request")
	}
	// 拒绝好友申请
	if err := s.svc.RejectFriendRequest(uint(req.RequestId), uint(req.OperatorUserId)); err != nil {
		log.Printf("[trace=%s] friend.RejectFriendRequest: failed: %v", traceID, err)
		switch {
		case errors.Is(err, friendservice.ErrRequestNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		case errors.Is(err, friendservice.ErrRequestStateInvalid):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "reject friend request failed: %v", err)
	}

	return &pbfriend.RejectFriendRequestResponse{Message: "rejected"}, nil
}

// BlockUser 处理“删除好友”请求（沿用原RPC名称以保持兼容）。
func (s *GRPCFriendServer) BlockUser(ctx context.Context, req *pbfriend.BlockUserRequest) (*pbfriend.BlockUserResponse, error) {
	traceID := traceIDFromCtx(ctx)
	// 非法请求
	if req == nil || req.UserId == 0 || req.BlockedUserId == 0 {
		log.Printf("[trace=%s] friend.BlockUser: invalid request", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "invalid request")
	}
	// 删除好友关系
	if err := s.svc.BlockUser(uint(req.UserId), uint(req.BlockedUserId)); err != nil {
		log.Printf("[trace=%s] friend.BlockUser: failed: %v", traceID, err)
		switch {
		case errors.Is(err, friendservice.ErrInvalidUserID), errors.Is(err, friendservice.ErrCannotAddSelf):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "remove friend failed: %v", err)
	}

	return &pbfriend.BlockUserResponse{Message: "friend removed"}, nil
}

// IsFriend 处理好友关系查询请求。
func (s *GRPCFriendServer) IsFriend(ctx context.Context, req *pbfriend.IsFriendRequest) (*pbfriend.IsFriendResponse, error) {
	traceID := traceIDFromCtx(ctx)
	// 非法请求
	if req == nil || req.UserId == 0 || req.TargetUserId == 0 {
		log.Printf("[trace=%s] friend.IsFriend: invalid request", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "invalid request")
	}
	// 查询好友关系
	ok, err := s.svc.IsFriend(uint(req.UserId), uint(req.TargetUserId))
	if err != nil {
		log.Printf("[trace=%s] friend.IsFriend: failed: %v", traceID, err)
		return nil, status.Errorf(codes.Internal, "is friend failed: %v", err)
	}

	return &pbfriend.IsFriendResponse{IsFriend: ok}, nil
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

// GetFriendRequestByID 查询好友申请详情。
func (s *GRPCFriendServer) GetFriendRequestByID(ctx context.Context, req *pbfriend.GetFriendRequestByIDRequest) (*pbfriend.GetFriendRequestByIDResponse, error) {
	traceID := traceIDFromCtx(ctx)
	// 非法请求
	if req == nil || req.RequestId == 0 {
		log.Printf("[trace=%s] friend.GetFriendRequestByID: invalid request", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "invalid request")
	}
	// 查询好友申请详情
	item, err := s.svc.GetFriendRequestByID(uint(req.RequestId))
	if err != nil {
		if err == friendservice.ErrRequestNotFound {
			return nil, status.Errorf(codes.NotFound, "friend request not found")
		}
		log.Printf("[trace=%s] friend.GetFriendRequestByID: failed: %v", traceID, err)
		return nil, status.Errorf(codes.Internal, "get friend request failed: %v", err)
	}
	// 转换为 protobuf 对象
	return &pbfriend.GetFriendRequestByIDResponse{
		Item: friendRequestToPB(item),
	}, nil
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

// friendRequestToPB 转换好友申请领域对象为 protobuf 对象。
func friendRequestToPB(fr *model.FriendRequest) *pbfriend.FriendRequestItem {
	if fr == nil {
		return nil
	}
	return &pbfriend.FriendRequestItem{
		RequestId:  uint64(fr.ID),
		FromUserId: uint64(fr.FromUserID),
		ToUserId:   uint64(fr.ToUserID),
		Status:     fr.Status,
		Remark:     fr.Remark,
		CreatedAt:  fr.CreatedAt.Unix(),
		UpdatedAt:  fr.UpdatedAt.Unix(),
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
