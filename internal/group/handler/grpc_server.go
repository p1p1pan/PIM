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

var _ pbgroup.GroupServiceServer = (*GRPCGroupServer)(nil)

type GRPCGroupServer struct {
	pbgroup.UnimplementedGroupServiceServer
	svc *groupservice.Service
}

func NewGRPCGroupServer(svc *groupservice.Service) *GRPCGroupServer {
	return &GRPCGroupServer{svc: svc}
}

func (s *GRPCGroupServer) CreateGroup(ctx context.Context, req *pbgroup.CreateGroupRequest) (*pbgroup.CreateGroupResponse, error) {
	// 非法请求
	if req == nil || req.OwnerUserId == 0 || req.Name == "" {
		return nil, status.Errorf(codes.InvalidArgument, "invalid request")
	}
	// 创建群
	g, err := s.svc.CreateGroup(uint(req.OwnerUserId), req.Name, req.MemberUserIds)
	if err != nil {
		log.Printf("group.CreateGroup failed: %v", err)
		if errors.Is(err, groupservice.ErrInvalidUserID) || errors.Is(err, groupservice.ErrInvalidGroupName) {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "create group failed: %v", err)
	}
	// 返回响应
	return &pbgroup.CreateGroupResponse{
		Group: &pbgroup.Group{
			Id:          uint64(g.ID),
			Name:        g.Name,
			OwnerUserId: uint64(g.OwnerUserID),
			Notice:      g.Notice,
			CreatedAt:   g.CreatedAt.Unix(),
			UpdatedAt:   g.UpdatedAt.Unix(),
		},
	}, nil
}

// AddMember 添加成员
func (s *GRPCGroupServer) AddMember(ctx context.Context, req *pbgroup.AddMemberRequest) (*pbgroup.AddMemberResponse, error) {
	// 非法请求
	if req == nil || req.GroupId == 0 || req.OperatorUserId == 0 || req.TargetUserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	// 添加成员
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

// RemoveMember 删除成员
func (s *GRPCGroupServer) RemoveMember(ctx context.Context, req *pbgroup.RemoveMemberRequest) (*pbgroup.RemoveMemberResponse, error) {
	// 非法请求
	if req == nil || req.GroupId == 0 || req.OperatorUserId == 0 || req.TargetUserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	// 删除成员
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

// ListMembers 列出成员
func (s *GRPCGroupServer) ListMembers(ctx context.Context, req *pbgroup.ListMembersRequest) (*pbgroup.ListMembersResponse, error) {
	// 非法请求
	if req == nil || req.GroupId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	// 列出成员
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
	// 返回响应
	items := make([]*pbgroup.GroupMember, 0, len(members))
	for i := range members {
		m := members[i]
		items = append(items, &pbgroup.GroupMember{
			Id:       uint64(m.ID),
			GroupId:  uint64(m.GroupID),
			UserId:   uint64(m.UserID),
			Role:     m.Role,
			JoinedAt: m.CreatedAt.Unix(),
		})
	}
	return &pbgroup.ListMembersResponse{Members: items}, nil
}

// IsMember 是否为成员
func (s *GRPCGroupServer) IsMember(ctx context.Context, req *pbgroup.IsMemberRequest) (*pbgroup.IsMemberResponse, error) {
	// 非法请求
	if req == nil || req.GroupId == 0 || req.UserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	// 是否为成员
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

// ListUserGroups 列出用户加入的群。
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
		g := groups[i]
		items = append(items, &pbgroup.Group{
			Id:          uint64(g.ID),
			Name:        g.Name,
			OwnerUserId: uint64(g.OwnerUserID),
			Notice:      g.Notice,
			CreatedAt:   g.CreatedAt.Unix(),
			UpdatedAt:   g.UpdatedAt.Unix(),
		})
	}
	return &pbgroup.ListUserGroupsResponse{Groups: items}, nil
}

// ListGroupMessages 列出群消息历史。
func (s *GRPCGroupServer) ListGroupMessages(ctx context.Context, req *pbgroup.ListGroupMessagesRequest) (*pbgroup.ListGroupMessagesResponse, error) {
	if req == nil || req.GroupId == 0 || req.UserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	msgs, hasMore, nextBefore, err := s.svc.ListGroupMessages(uint(req.GroupId), uint(req.UserId), req.GetBeforeSeq(), int(req.Limit))
	if err != nil {
		log.Printf("group.ListGroupMessages failed: %v", err)
		switch {
		case errors.Is(err, groupservice.ErrInvalidGroupID), errors.Is(err, groupservice.ErrInvalidUserID):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, groupservice.ErrNotGroupMember):
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "list group messages failed: %v", err)
		}
	}
	items := make([]*pbgroup.GroupMessageItem, 0, len(msgs))
	for i := range msgs {
		m := msgs[i]
		items = append(items, &pbgroup.GroupMessageItem{
			Id:          uint64(m.ID),
			GroupId:     uint64(m.GroupID),
			FromUserId:  uint64(m.FromUserID),
			MessageType: m.MessageType,
			Content:     m.Content,
			Seq:         m.Seq,
			CreatedAt:   m.CreatedAt.Unix(),
		})
	}
	return &pbgroup.ListGroupMessagesResponse{
		Messages:      items,
		HasMore:       hasMore,
		NextBeforeSeq: nextBefore,
	}, nil
}

// GetGroup 查询群详情。
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
	return &pbgroup.GetGroupResponse{
		Group: &pbgroup.Group{
			Id:          uint64(g.ID),
			Name:        g.Name,
			OwnerUserId: uint64(g.OwnerUserID),
			Notice:      g.Notice,
			CreatedAt:   g.CreatedAt.Unix(),
			UpdatedAt:   g.UpdatedAt.Unix(),
		},
	}, nil
}

// UpdateGroup 更新群资料。
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
	return &pbgroup.UpdateGroupResponse{
		Group: &pbgroup.Group{
			Id:          uint64(g.ID),
			Name:        g.Name,
			OwnerUserId: uint64(g.OwnerUserID),
			Notice:      g.Notice,
			CreatedAt:   g.CreatedAt.Unix(),
			UpdatedAt:   g.UpdatedAt.Unix(),
		},
	}, nil
}

// LeaveGroup 退出群。
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

// DisbandGroup 解散群。
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

// TransferOwner 转让群主。
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
	return &pbgroup.TransferOwnerResponse{
		Group: &pbgroup.Group{
			Id:          uint64(g.ID),
			Name:        g.Name,
			OwnerUserId: uint64(g.OwnerUserID),
			Notice:      g.Notice,
			CreatedAt:   g.CreatedAt.Unix(),
			UpdatedAt:   g.UpdatedAt.Unix(),
		},
	}, nil
}

// AppendSystemMessage 追加群系统消息。
func (s *GRPCGroupServer) AppendSystemMessage(ctx context.Context, req *pbgroup.AppendSystemMessageRequest) (*pbgroup.AppendSystemMessageResponse, error) {
	if req == nil || req.GroupId == 0 || req.OperatorUserId == 0 || req.EventType == "" || req.Content == "" || req.EventId == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	msg, err := s.svc.AppendSystemMessage(uint(req.GroupId), uint(req.OperatorUserId), req.EventType, req.Content, req.EventId)
	if err != nil {
		log.Printf("group.AppendSystemMessage failed: %v", err)
		switch {
		case errors.Is(err, groupservice.ErrInvalidGroupID), errors.Is(err, groupservice.ErrInvalidUserID), errors.Is(err, groupservice.ErrInvalidEventType), errors.Is(err, groupservice.ErrInvalidMessageContent), errors.Is(err, groupservice.ErrInvalidEventID):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, groupservice.ErrNotGroupMember):
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "append system message failed: %v", err)
		}
	}
	return &pbgroup.AppendSystemMessageResponse{Seq: msg.Seq}, nil
}

// MarkGroupRead 上报群已读。
func (s *GRPCGroupServer) MarkGroupRead(ctx context.Context, req *pbgroup.MarkGroupReadRequest) (*pbgroup.MarkGroupReadResponse, error) {
	if req == nil || req.GroupId == 0 || req.UserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	readSeq, err := s.svc.MarkGroupRead(uint(req.GroupId), uint(req.UserId), req.ReadSeq)
	if err != nil {
		log.Printf("group.MarkGroupRead failed: %v", err)
		switch {
		case errors.Is(err, groupservice.ErrInvalidGroupID), errors.Is(err, groupservice.ErrInvalidUserID):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, groupservice.ErrNotGroupMember):
			return nil, status.Error(codes.PermissionDenied, err.Error())
		default:
			return nil, status.Errorf(codes.Internal, "mark group read failed: %v", err)
		}
	}
	return &pbgroup.MarkGroupReadResponse{ReadSeq: readSeq}, nil
}

// ListUserGroupConversations 列出用户群会话（含未读）。
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
		g := it.Group
		out = append(out, &pbgroup.GroupConversationItem{
			Group: &pbgroup.Group{
				Id:          uint64(g.ID),
				Name:        g.Name,
				OwnerUserId: uint64(g.OwnerUserID),
				Notice:      g.Notice,
				CreatedAt:   g.CreatedAt.Unix(),
				UpdatedAt:   g.UpdatedAt.Unix(),
			},
			LastSeq:     it.LastSeq,
			ReadSeq:     it.ReadSeq,
			UnreadCount: it.UnreadCount,
		})
	}
	return &pbgroup.ListUserGroupConversationsResponse{Items: out}, nil
}
