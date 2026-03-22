package handler

import (
	"context"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pbauth "pim/internal/auth/pb"
	authservice "pim/internal/auth/service"
	pbuser "pim/internal/user/pb"
)

var _ pbauth.AuthServiceServer = (*GRPCAuthServer)(nil)

// GRPCAuthServer 实现 Auth gRPC 接口，负责登录与 token 校验。
type GRPCAuthServer struct {
	pbauth.UnimplementedAuthServiceServer
	userClient pbuser.UserServiceClient
}

// NewGRPCAuthServer 创建 Auth gRPC 处理器。
func NewGRPCAuthServer(userClient pbuser.UserServiceClient) *GRPCAuthServer {
	return &GRPCAuthServer{userClient: userClient}
}

// ValidateToken 校验 JWT 并返回 user_id。
func (s *GRPCAuthServer) ValidateToken(ctx context.Context, req *pbauth.ValidateTokenRequest) (*pbauth.ValidateTokenResponse, error) {
	traceID := traceIDFromCtx(ctx)
	if req == nil || req.Token == "" {
		log.Printf("[trace=%s] auth.ValidateToken: missing token", traceID)
		return nil, status.Error(codes.Unauthenticated, "missing token")
	}
	userID, _, err := authservice.ParseToken(req.Token)
	if err != nil {
		log.Printf("[trace=%s] auth.ValidateToken: invalid token: %v", traceID, err)
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
	return &pbauth.ValidateTokenResponse{UserId: uint64(userID)}, nil
}

// Login 通过 user-service 校验用户名密码并返回认证信息。
func (s *GRPCAuthServer) Login(ctx context.Context, req *pbauth.LoginRequest) (*pbauth.LoginResponse, error) {
	traceID := traceIDFromCtx(ctx)
	if req == nil || req.Username == "" || req.Password == "" {
		log.Printf("[trace=%s] auth.Login: missing username or password", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "username and password are required")
	}
	userResp, err := s.userClient.Login(ctx, &pbuser.LoginRequest{
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		log.Printf("[trace=%s] auth.Login: user.Login failed: %v", traceID, err)
		if st, ok := status.FromError(err); ok {
			return nil, status.Error(st.Code(), st.Message())
		}
		return nil, status.Errorf(codes.Unauthenticated, "login failed")
	}
	// Auth 透传 user-service 的 token，不在此层重复签发，避免双 token 源。
	u := userResp.GetUser()
	return &pbauth.LoginResponse{
		Message:     userResp.GetMessage(),
		AccessToken: userResp.GetToken(),
		UserId:      u.GetId(),
		Username:    u.GetUsername(),
	}, nil
}

// traceIDFromCtx 从 gRPC metadata 里提取 x-trace-id。
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
