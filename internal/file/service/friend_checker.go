package service

import (
	"context"

	pbfriend "pim/internal/friend/pb"
)

// FriendGRPCChecker 通过 friend-service 的 IsFriend RPC 校验好友关系。
type FriendGRPCChecker struct {
	client pbfriend.FriendServiceClient
}

func NewFriendGRPCChecker(client pbfriend.FriendServiceClient) *FriendGRPCChecker {
	return &FriendGRPCChecker{client: client}
}

func (c *FriendGRPCChecker) IsFriend(ctx context.Context, userID, targetUserID uint) (bool, error) {
	if c == nil || c.client == nil || userID == 0 || targetUserID == 0 {
		return false, nil
	}
	resp, err := c.client.IsFriend(ctx, &pbfriend.IsFriendRequest{
		UserId:       uint64(userID),
		TargetUserId: uint64(targetUserID),
	})
	if err != nil {
		return false, err
	}
	return resp.GetIsFriend(), nil
}
