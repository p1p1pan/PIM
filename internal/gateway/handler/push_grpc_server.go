package handler

import (
	"context"
	"log"

	conversationhandler "pim/internal/conversation/handler"
	pbgateway "pim/internal/gateway/pb"
)

type PushServiceServer struct {
	pbgateway.UnimplementedPushServiceServer
}

// NewPushServiceServer 创建 Gateway 内部推送 gRPC 服务。
func NewPushServiceServer() *PushServiceServer { return &PushServiceServer{} }

// PushToConn 把消息下行到目标用户的 WebSocket 连接。
func (s *PushServiceServer) PushToConn(ctx context.Context, req *pbgateway.PushToConnRequest) (*pbgateway.PushToConnResponse, error) {
	from := uint(req.GetFromUserId())
	to := uint(req.GetToUserId())
	content := req.GetContent()
	if err := conversationhandler.PushToUser(to, from, content); err != nil {
		log.Printf("PushToConn: failed to push from=%d to=%d: %v", from, to, err)
		return &pbgateway.PushToConnResponse{Ok: false, Error: err.Error()}, nil
	}
	return &pbgateway.PushToConnResponse{Ok: true, Error: ""}, nil
}
