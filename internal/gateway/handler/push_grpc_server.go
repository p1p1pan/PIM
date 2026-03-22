package handler

import (
	"context"
	"log"

	conversationhandler "pim/internal/conversation/handler"
	pbgateway "pim/internal/gateway/pb"
	observemetrics "pim/internal/observability/metrics"
)

type PushServiceServer struct {
	pbgateway.UnimplementedPushServiceServer
}

// NewPushServiceServer 创建 Gateway 内部推送 gRPC 服务。
func NewPushServiceServer() *PushServiceServer { return &PushServiceServer{} }

// PushToConn 把消息下行到目标用户的 WebSocket 连接。
func (s *PushServiceServer) PushToConn(ctx context.Context, req *pbgateway.PushToConnRequest) (*pbgateway.PushToConnResponse, error) {
	_ = ctx // 预留 trace/timeout 扩展位，当前直接走本地 ws 推送。
	from := uint(req.GetFromUserId())
	to := uint(req.GetToUserId())
	content := req.GetContent()
	// PushToUser 只推当前 gateway 进程内连接；跨节点由上游先做路由。
	if err := conversationhandler.PushToUser(to, from, content); err != nil {
		observemetrics.ObserveGatewayPush("error")
		log.Printf("PushToConn: failed to push from=%d to=%d: %v", from, to, err)
		return &pbgateway.PushToConnResponse{Ok: false, Error: err.Error()}, nil
	}
	observemetrics.ObserveGatewayPush("ok")
	return &pbgateway.PushToConnResponse{Ok: true, Error: ""}, nil
}
