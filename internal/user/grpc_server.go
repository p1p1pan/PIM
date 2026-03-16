package user

import (
	"context"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pbuser "pim/internal/user/pb"
)

var _ pbuser.UserServiceServer = (*GRPCUserServer)(nil)

type GRPCUserServer struct {
	pbuser.UnimplementedUserServiceServer
	svc *Service
}

// Register 注册用户
func (s *GRPCUserServer) Register(ctx context.Context, req *pbuser.RegisterRequest) (*pbuser.RegisterResponse, error) {
	traceID := traceIDFromCtx(ctx)
	if req == nil || req.Username == "" || req.Password == "" {
		log.Printf("[trace=%s] user.Register: missing username or password", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "username and password are required")
	}
	u, err := s.svc.Register(req.Username, req.Password)
	if err != nil {
		log.Printf("[trace=%s] user.Register: failed to register user: %v", traceID, err)
		return nil, status.Errorf(codes.Internal, "failed to register user: %v", err)
	}
	return &pbuser.RegisterResponse{
		Message: "User registered successfully",
		User:    userToPB(u),
	}, nil
}

// GetByID 根据用户 ID 获取用户信息
func (s *GRPCUserServer) GetByID(ctx context.Context, req *pbuser.GetByIDRequest) (*pbuser.GetByIDResponse, error) {
	traceID := traceIDFromCtx(ctx)
	if req == nil || req.UserId == 0 {
		log.Printf("[trace=%s] user.GetByID: missing user id", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "user id is required")
	}
	u, err := s.svc.GetByID(uint(req.UserId))
	if err != nil {
		log.Printf("[trace=%s] user.GetByID: failed to get user by id: %v", traceID, err)
		return nil, status.Errorf(codes.Internal, "failed to get user by id: %v", err)
	}
	return &pbuser.GetByIDResponse{User: userToPB(u)}, nil
}

// Login 登录用户
func (s *GRPCUserServer) Login(ctx context.Context, req *pbuser.LoginRequest) (*pbuser.LoginResponse, error) {
	traceID := traceIDFromCtx(ctx)
	// 验证请求
	if req == nil || req.Username == "" || req.Password == "" {
		log.Printf("[trace=%s] user.Login: missing username or password", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "username and password are required")
	}
	// 调用服务
	u, token, err := s.svc.Login(req.Username, req.Password)
	if err != nil {
		log.Printf("[trace=%s] user.Login: login failed: %v", traceID, err)
		return nil, status.Errorf(codes.Unauthenticated, "%v", err)
	}
	// 返回响应
	return &pbuser.LoginResponse{
		Message: "Login successful",
		Token:   token,
		User:    userToPB(u),
	}, nil
}

// NewGRPCUserServer 用 db 构造 gRPC 服务端，供 cmd/user-service 注册。
func NewGRPCUserServer(db *gorm.DB) *GRPCUserServer {
	return &GRPCUserServer{svc: NewService(db)}
}

// userToPB 把 internal User 转成 pb User（不含密码），时间用 Unix 秒。
func userToPB(u *User) *pbuser.User {
	if u == nil {
		return nil
	}
	return &pbuser.User{
		Id:        uint64(u.ID),
		Username:  u.Username,
		Nickname:  u.Nickname,
		AvatarUrl: u.AvatarURL,
		Bio:       u.Bio,
		CreatedAt: u.CreatedAt.Unix(),
		UpdatedAt: u.UpdatedAt.Unix(),
	}
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
