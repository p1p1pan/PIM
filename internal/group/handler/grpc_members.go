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

func (s *GRPCGroupServer) AddMember(ctx context.Context, req *pbgroup.AddMemberRequest) (*pbgroup.AddMemberResponse, error) {
	if req == nil || req.GroupId == 0 || req.OperatorUserId == 0 || req.TargetUserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	// 业务层会校验 operator 是否群主、target 是否已在群内。
	err := s.svc.AddMember(uint(req.GroupId), uint(req.OperatorUserId), uint(req.TargetUserId))
	if err != nil {
		log.Printf("group.AddMember failed: %v", err)
		switch {
		case errors.Is(err, groupservice.ErrInvalidGroupID), errors.Is(err, groupservice.ErrInvalidUserID):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, groupservice.ErrGroupNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		case errors.Is(err, groupservice.ErrNotGroupOwner):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "add member failed: %v", err)
		}
	}
	return &pbgroup.AddMemberResponse{Message: "ok"}, nil
}

func (s *GRPCGroupServer) RemoveMember(ctx context.Context, req *pbgroup.RemoveMemberRequest) (*pbgroup.RemoveMemberResponse, error) {
	if req == nil || req.GroupId == 0 || req.OperatorUserId == 0 || req.TargetUserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	// 删除成员同样要求群主权限，错误码映射在此层统一处理。
	err := s.svc.RemoveMember(uint(req.GroupId), uint(req.OperatorUserId), uint(req.TargetUserId))
	if err != nil {
		log.Printf("group.RemoveMember failed: %v", err)
		switch {
		case errors.Is(err, groupservice.ErrInvalidGroupID), errors.Is(err, groupservice.ErrInvalidUserID):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, groupservice.ErrGroupNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		case errors.Is(err, groupservice.ErrNotGroupOwner):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "remove member failed: %v", err)
		}
	}
	return &pbgroup.RemoveMemberResponse{Message: "ok"}, nil
}

func (s *GRPCGroupServer) ListMembers(ctx context.Context, req *pbgroup.ListMembersRequest) (*pbgroup.ListMembersResponse, error) {
	if req == nil || req.GroupId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	// handler 层只做协议适配：service 返回 domain model，这里转换成 pb。
	members, err := s.svc.ListMembers(uint(req.GroupId))
	if err != nil {
		log.Printf("group.ListMembers failed: %v", err)
		switch {
		case errors.Is(err, groupservice.ErrInvalidGroupID):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, groupservice.ErrGroupNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "list members failed: %v", err)
		}
	}
	return &pbgroup.ListMembersResponse{Members: toPBGroupMembers(members)}, nil
}

func (s *GRPCGroupServer) IsMember(ctx context.Context, req *pbgroup.IsMemberRequest) (*pbgroup.IsMemberResponse, error) {
	if req == nil || req.GroupId == 0 || req.UserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	ok, err := s.svc.IsMember(uint(req.GroupId), uint(req.UserId))
	if err != nil {
		log.Printf("group.IsMember failed: %v", err)
		switch {
		case errors.Is(err, groupservice.ErrInvalidGroupID), errors.Is(err, groupservice.ErrInvalidUserID):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "is member failed: %v", err)
		}
	}
	return &pbgroup.IsMemberResponse{IsMember: ok}, nil
}
