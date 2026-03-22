package handler

import (
	"context"
	"log"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"pim/internal/user/model"
	pbuser "pim/internal/user/pb"
	userservice "pim/internal/user/service"
)

var _ pbuser.UserServiceServer = (*GRPCUserServer)(nil)

// GRPCUserServer 实现 user gRPC 协议。
type GRPCUserServer struct {
	pbuser.UnimplementedUserServiceServer
	svc *userservice.Service
}

// NewGRPCUserServer 创建 user gRPC 处理器。
func NewGRPCUserServer(svc *userservice.Service) *GRPCUserServer {
	return &GRPCUserServer{svc: svc}
}

// Register 处理用户注册请求。
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
	// 协议层只做输入校验与错误映射，密码加密等逻辑由 service 处理。
	return &pbuser.RegisterResponse{Message: "User registered successfully", User: userToPB(u)}, nil
}

// GetByID 按 ID 查询用户。
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

// Login 处理登录并返回 token。
func (s *GRPCUserServer) Login(ctx context.Context, req *pbuser.LoginRequest) (*pbuser.LoginResponse, error) {
	traceID := traceIDFromCtx(ctx)
	if req == nil || req.Username == "" || req.Password == "" {
		log.Printf("[trace=%s] user.Login: missing username or password", traceID)
		return nil, status.Errorf(codes.InvalidArgument, "username and password are required")
	}
	u, token, err := s.svc.Login(req.Username, req.Password)
	if err != nil {
		log.Printf("[trace=%s] user.Login: login failed: %v", traceID, err)
		return nil, status.Errorf(codes.Unauthenticated, "%v", err)
	}
	// token 在 service 生成，这里仅负责组装返回协议。
	return &pbuser.LoginResponse{Message: "Login successful", Token: token, User: userToPB(u)}, nil
}

// userToPB 将领域模型转换为 protobuf 对象。
func userToPB(u *model.User) *pbuser.User {
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

// traceIDFromCtx 从 gRPC metadata 里提取 trace_id。
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
