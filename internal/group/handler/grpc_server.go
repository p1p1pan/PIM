package handler

import (
	"time"

	groupmodel "pim/internal/group/model"
	pbgroup "pim/internal/group/pb"
	groupservice "pim/internal/group/service"
)

var _ pbgroup.GroupServiceServer = (*GRPCGroupServer)(nil)

type GRPCGroupServer struct {
	pbgroup.UnimplementedGroupServiceServer
	svc *groupservice.Service
}

// NewGRPCGroupServer 创建 Group gRPC 处理器。
func NewGRPCGroupServer(svc *groupservice.Service) *GRPCGroupServer {
	return &GRPCGroupServer{svc: svc}
}

// toPBGroup 把领域层 Group 映射为 protobuf 对象。
func toPBGroup(g groupmodel.Group) *pbgroup.Group {
	return &pbgroup.Group{
		Id:          uint64(g.ID),
		Name:        g.Name,
		OwnerUserId: uint64(g.OwnerUserID),
		Notice:      g.Notice,
		CreatedAt:   toUnix(g.CreatedAt),
		UpdatedAt:   toUnix(g.UpdatedAt),
	}
}

// toPBGroupMembers 批量转换成员列表，避免各 RPC 重复拼装。
func toPBGroupMembers(members []groupmodel.GroupMember) []*pbgroup.GroupMember {
	out := make([]*pbgroup.GroupMember, 0, len(members))
	for i := range members {
		m := members[i]
		out = append(out, &pbgroup.GroupMember{
			Id:       uint64(m.ID),
			GroupId:  uint64(m.GroupID),
			UserId:   uint64(m.UserID),
			Role:     m.Role,
			JoinedAt: toUnix(m.CreatedAt),
		})
	}
	return out
}

// toPBGroupMessages 批量转换群消息列表。
func toPBGroupMessages(msgs []groupmodel.GroupMessage) []*pbgroup.GroupMessageItem {
	out := make([]*pbgroup.GroupMessageItem, 0, len(msgs))
	for i := range msgs {
		m := msgs[i]
		out = append(out, &pbgroup.GroupMessageItem{
			Id:          uint64(m.ID),
			GroupId:     uint64(m.GroupID),
			FromUserId:  uint64(m.FromUserID),
			MessageType: m.MessageType,
			Content:     m.Content,
			Seq:         m.Seq,
			CreatedAt:   toUnix(m.CreatedAt),
			MentionMeta: m.MentionMeta,
		})
	}
	return out
}

// toUnix 统一时间戳转换，便于后续替换时间格式策略。
func toUnix(t time.Time) int64 {
	return t.Unix()
}
