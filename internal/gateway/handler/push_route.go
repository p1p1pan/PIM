package handler

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	conversationhandler "pim/internal/conversation/handler"
	pbgateway "pim/internal/gateway/pb"
	"pim/internal/platform/registry"
)

// pushContentRouted routes a push via Redis ws:conn (local first, then PushService).
// Offline / missing route returns nil.
func (s *HTTPServer) pushContentRouted(ctx context.Context, toUserID uint, fromUserID uint, content string) error {
	if toUserID == 0 || strings.TrimSpace(content) == "" {
		return nil
	}
	if err := conversationhandler.PushToUser(toUserID, fromUserID, content); err == nil {
		return nil
	}
	if s == nil || s.redisClient == nil {
		return fmt.Errorf("user %d not on this gateway and redis unavailable", toUserID)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	route, err := s.redisClient.Get(ctx, fmt.Sprintf("ws:conn:%d", toUserID)).Result()
	if err != nil || strings.TrimSpace(route) == "" {
		return nil
	}
	parts := strings.SplitN(route, ":", 2)
	node := strings.TrimSpace(parts[0])
	if node == "" {
		return fmt.Errorf("malformed ws route for user %d", toUserID)
	}
	if node == s.nodeID {
		return nil
	}
	client := s.pushClientForNode(node)
	if client == nil {
		return fmt.Errorf("no push client for gateway node %s", node)
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	resp, err := client.PushToConn(cctx, &pbgateway.PushToConnRequest{
		FromUserId: uint64(fromUserID),
		ToUserId:   uint64(toUserID),
		Content:    content,
	})
	if err != nil {
		return err
	}
	if resp != nil && !resp.GetOk() {
		return fmt.Errorf("push rejected: %s", resp.GetError())
	}
	return nil
}

func (s *HTTPServer) pushClientForNode(node string) pbgateway.PushServiceClient {
	if s == nil || s.pushWatcher == nil || node == "" {
		return nil
	}
	clients := s.pushWatcher.Snapshot()
	return clients[node]
}

func (s *HTTPServer) ensurePushWatcher() {
	if s == nil || s.pushWatcher != nil {
		return
	}
	cli, err := registry.EtcdClient(context.Background())
	if err != nil {
		log.Printf("gateway push watcher: etcd unavailable: %v", err)
		return
	}
	w, err := registry.StartGatewayPushWatcher(context.Background(), cli)
	if err != nil {
		log.Printf("gateway push watcher: start failed: %v", err)
		return
	}
	s.pushWatcher = w
}
