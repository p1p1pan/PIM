package auth

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pbauth "pim/internal/auth/pb"
)

var _ pbauth.AuthServiceServer = (*GRPCAuthServer)(nil)

type GRPCAuthServer struct {
	pbauth.UnimplementedAuthServiceServer
}

func (s *GRPCAuthServer) ValidateToken(ctx context.Context, req *pbauth.ValidateTokenRequest) (*pbauth.ValidateTokenResponse, error) {
	if req == nil || req.Token == "" {
		return nil, status.Error(codes.Unauthenticated, "missing token")
	}
	userID, _, err := ParseToken(req.Token)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
	return &pbauth.ValidateTokenResponse{UserId: uint64(userID)}, nil
}