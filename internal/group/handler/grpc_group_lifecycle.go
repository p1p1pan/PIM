package handler

import (
	"context"
	"errors"
	"log"

	pbgroup "pim/internal/group/pb"
	groupservice "pim/internal/group/service"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *GRPCGroupServer) CreateGroup(ctx context.Context, req *pbgroup.CreateGroupRequest) (*pbgroup.CreateGroupResponse, error) {
	if req == nil || req.OwnerUserId == 0 || req.Name == "" {
		return nil, status.Errorf(codes.InvalidArgument, "invalid request")
	}
	g, err := s.svc.CreateGroup(uint(req.OwnerUserId), req.Name, req.MemberUserIds)
	if err != nil {
		log.Printf("group.CreateGroup failed: %v", err)
		if errors.Is(err, groupservice.ErrInvalidUserID) || errors.Is(err, groupservice.ErrInvalidGroupName) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "create group failed: %v", err)
	}
	if g == nil {
		return nil, status.Error(codes.Internal, "create group returned nil")
	}
	// 统一复用转换函数，避免每个接口重复拼装 pbgroup.Group。
	return &pbgroup.CreateGroupResponse{Group: toPBGroup(*g)}, nil
}

func (s *GRPCGroupServer) ListUserGroups(ctx context.Context, req *pbgroup.ListUserGroupsRequest) (*pbgroup.ListUserGroupsResponse, error) {
	if req == nil || req.UserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	groups, err := s.svc.ListUserGroups(uint(req.UserId))
	if err != nil {
		log.Printf("group.ListUserGroups failed: %v", err)
		if errors.Is(err, groupservice.ErrInvalidUserID) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "list user groups failed: %v", err)
	}
	items := make([]*pbgroup.Group, 0, len(groups))
	for i := range groups {
		// 这里保持按 service 返回顺序输出，前端可直接按 updated_at 展示。
		items = append(items, toPBGroup(groups[i]))
	}
	return &pbgroup.ListUserGroupsResponse{Groups: items}, nil
}

func (s *GRPCGroupServer) GetGroup(ctx context.Context, req *pbgroup.GetGroupRequest) (*pbgroup.GetGroupResponse, error) {
	if req == nil || req.GroupId == 0 || req.OperatorUserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	g, err := s.svc.GetGroup(uint(req.GroupId), uint(req.OperatorUserId))
	if err != nil {
		log.Printf("group.GetGroup failed: %v", err)
		switch {
		case errors.Is(err, groupservice.ErrInvalidGroupID), errors.Is(err, groupservice.ErrInvalidUserID):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, groupservice.ErrGroupNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		case errors.Is(err, groupservice.ErrNotGroupMember):
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "get group failed: %v", err)
		}
	}
	if g == nil {
		return nil, status.Error(codes.NotFound, "group not found")
	}
	return &pbgroup.GetGroupResponse{Group: toPBGroup(*g)}, nil
}

func (s *GRPCGroupServer) UpdateGroup(ctx context.Context, req *pbgroup.UpdateGroupRequest) (*pbgroup.UpdateGroupResponse, error) {
	if req == nil || req.GroupId == 0 || req.OperatorUserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	g, err := s.svc.UpdateGroup(uint(req.GroupId), uint(req.OperatorUserId), req.Name, req.Notice)
	if err != nil {
		log.Printf("group.UpdateGroup failed: %v", err)
		switch {
		case errors.Is(err, groupservice.ErrInvalidGroupID), errors.Is(err, groupservice.ErrInvalidUserID), errors.Is(err, groupservice.ErrInvalidGroupName):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, groupservice.ErrGroupNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		case errors.Is(err, groupservice.ErrNotGroupOwner):
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "update group failed: %v", err)
		}
	}
	if g == nil {
		return nil, status.Error(codes.NotFound, "group not found")
	}
	return &pbgroup.UpdateGroupResponse{Group: toPBGroup(*g)}, nil
}

func (s *GRPCGroupServer) LeaveGroup(ctx context.Context, req *pbgroup.LeaveGroupRequest) (*pbgroup.LeaveGroupResponse, error) {
	if req == nil || req.GroupId == 0 || req.OperatorUserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	if err := s.svc.LeaveGroup(uint(req.GroupId), uint(req.OperatorUserId)); err != nil {
		log.Printf("group.LeaveGroup failed: %v", err)
		switch {
		case errors.Is(err, groupservice.ErrInvalidGroupID), errors.Is(err, groupservice.ErrInvalidUserID):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, groupservice.ErrGroupNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		case errors.Is(err, groupservice.ErrNotGroupMember), errors.Is(err, groupservice.ErrCannotLeaveAsOwner):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "leave group failed: %v", err)
		}
	}
	return &pbgroup.LeaveGroupResponse{Message: "ok"}, nil
}

func (s *GRPCGroupServer) DisbandGroup(ctx context.Context, req *pbgroup.DisbandGroupRequest) (*pbgroup.DisbandGroupResponse, error) {
	if req == nil || req.GroupId == 0 || req.OperatorUserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	if err := s.svc.DisbandGroup(uint(req.GroupId), uint(req.OperatorUserId)); err != nil {
		log.Printf("group.DisbandGroup failed: %v", err)
		switch {
		case errors.Is(err, groupservice.ErrInvalidGroupID), errors.Is(err, groupservice.ErrInvalidUserID):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, groupservice.ErrGroupNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		case errors.Is(err, groupservice.ErrNotGroupOwner):
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "disband group failed: %v", err)
		}
	}
	return &pbgroup.DisbandGroupResponse{Message: "ok"}, nil
}

func (s *GRPCGroupServer) TransferOwner(ctx context.Context, req *pbgroup.TransferOwnerRequest) (*pbgroup.TransferOwnerResponse, error) {
	if req == nil || req.GroupId == 0 || req.OperatorUserId == 0 || req.TargetUserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	g, err := s.svc.TransferOwner(uint(req.GroupId), uint(req.OperatorUserId), uint(req.TargetUserId))
	if err != nil {
		log.Printf("group.TransferOwner failed: %v", err)
		switch {
		case errors.Is(err, groupservice.ErrInvalidGroupID), errors.Is(err, groupservice.ErrInvalidUserID):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, groupservice.ErrGroupNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		case errors.Is(err, groupservice.ErrNotGroupOwner), errors.Is(err, groupservice.ErrNotGroupMember):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "transfer owner failed: %v", err)
		}
	}
	if g == nil {
		return nil, status.Error(codes.NotFound, "group not found")
	}
	return &pbgroup.TransferOwnerResponse{Group: toPBGroup(*g)}, nil
}

func (s *GRPCGroupServer) ListUserGroupConversations(ctx context.Context, req *pbgroup.ListUserGroupConversationsRequest) (*pbgroup.ListUserGroupConversationsResponse, error) {
	if req == nil || req.UserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	items, err := s.svc.ListUserGroupConversations(uint(req.UserId))
	if err != nil {
		log.Printf("group.ListUserGroupConversations failed: %v", err)
		if errors.Is(err, groupservice.ErrInvalidUserID) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "list user group conversations failed: %v", err)
	}
	out := make([]*pbgroup.GroupConversationItem, 0, len(items))
	for _, it := range items {
		// 会话聚合结果由 service 计算，这里只负责协议层映射。
		out = append(out, &pbgroup.GroupConversationItem{
			Group:       toPBGroup(it.Group),
			LastSeq:     it.LastSeq,
			ReadSeq:     it.ReadSeq,
			UnreadCount: it.UnreadCount,
		})
	}
	return &pbgroup.ListUserGroupConversationsResponse{Items: out}, nil
}
