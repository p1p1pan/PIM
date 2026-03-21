package service

import (
	"context"

	pbgroup "pim/internal/group/pb"
)

// GroupGRPCChecker 通过 group-service 的 IsMember RPC 做成员校验。
type GroupGRPCChecker struct {
	client pbgroup.GroupServiceClient
}

func NewGroupGRPCChecker(client pbgroup.GroupServiceClient) *GroupGRPCChecker {
	return &GroupGRPCChecker{client: client}
}

func (c *GroupGRPCChecker) IsMember(ctx context.Context, groupID, userID uint) (bool, error) {
	if c == nil || c.client == nil || groupID == 0 || userID == 0 {
		return false, nil
	}
	resp, err := c.client.IsMember(ctx, &pbgroup.IsMemberRequest{
		GroupId: uint64(groupID),
		UserId:  uint64(userID),
	})
	if err != nil {
		return false, err
	}
	return resp.GetIsMember(), nil
}
