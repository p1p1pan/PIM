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

func (s *GRPCGroupServer) ListGroupMessages(ctx context.Context, req *pbgroup.ListGroupMessagesRequest) (*pbgroup.ListGroupMessagesResponse, error) {
	if req == nil || req.GroupId == 0 || req.UserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	// service 已完成成员权限与分页游标处理，handler 仅做错误码映射。
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
	return &pbgroup.ListGroupMessagesResponse{
		Messages:      toPBGroupMessages(msgs),
		HasMore:       hasMore,
		NextBeforeSeq: nextBefore,
	}, nil
}

func (s *GRPCGroupServer) AppendSystemMessage(ctx context.Context, req *pbgroup.AppendSystemMessageRequest) (*pbgroup.AppendSystemMessageResponse, error) {
	if req == nil || req.GroupId == 0 || req.OperatorUserId == 0 || req.EventType == "" || req.Content == "" || req.EventId == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	// event_id 由调用方提供，用于幂等写入和跨服务追踪。
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

func (s *GRPCGroupServer) MarkGroupRead(ctx context.Context, req *pbgroup.MarkGroupReadRequest) (*pbgroup.MarkGroupReadResponse, error) {
	if req == nil || req.GroupId == 0 || req.UserId == 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	// read_seq 由 service 做“只前进不回退”保护。
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
