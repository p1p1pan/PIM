package auth

import (
	"context"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pbauth "pim/internal/auth/pb"
	pbuser "pim/internal/user/pb"
)

var _ pbauth.AuthServiceServer = (*GRPCAuthServer)(nil)

type GRPCAuthServer struct {
	pbauth.UnimplementedAuthServiceServer
	userClient pbuser.UserServiceClient
}

func NewGRPCAuthServer(userClient pbuser.UserServiceClient) *GRPCAuthServer {
	return &GRPCAuthServer{
		userClient: userClient,
	}
}

func (s *GRPCAuthServer) ValidateToken(ctx context.Context, req *pbauth.ValidateTokenRequest) (*pbauth.ValidateTokenResponse, error) {
	traceID := traceIDFromCtx(ctx)
	if req == nil || req.Token == "" {
		log.Printf("[trace=%s] auth.ValidateToken: missing token", traceID)
		return nil, status.Error(codes.Unauthenticated, "missing token")
	}
	userID, _, err := ParseToken(req.Token)
	if err != nil {
		log.Printf("[trace=%s] auth.ValidateToken: invalid token: %v", traceID, err)
		return nil, status.Error(codes.Unauthenticated, "invalid token")
	}
	return &pbauth.ValidateTokenResponse{UserId: uint64(userID)}, nil
}

// traceIDFromCtx 从 gRPC metadata 中提取 x-trace-id。
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

// Login 登录：调用 user 服务的 Login 校验用户名密码与签 token，再包装成 Auth 的 LoginResponse。
func (s *GRPCAuthServer) Login(ctx context.Context, req *pbauth.LoginRequest) (*pbauth.LoginResponse, error) {
	traceID := traceIDFromCtx(ctx)
	if req == nil || req.Username == "" || req.Password == "" {
		log.Printf("[trace=%s] auth.Login: missing username or password", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "username and password are required")
	}
	// 调用 user gRPC Login
	userResp, err := s.userClient.Login(ctx, &pbuser.LoginRequest{
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		log.Printf("[trace=%s] auth.Login: user.Login failed: %v", traceID, err)
		// 直接透传 user 的错误码更合理，这里简单按 Unauthenticated 处理
		if st, ok := status.FromError(err); ok {
			return nil, status.Error(st.Code(), st.Message())
		}
		return nil, status.Errorf(codes.Unauthenticated, "login failed")
	}
	u := userResp.GetUser()
	return &pbauth.LoginResponse{
		Message:     userResp.GetMessage(),
		AccessToken: userResp.GetToken(),
		UserId:      u.GetId(),
		Username:    u.GetUsername(),
	}, nil
}
