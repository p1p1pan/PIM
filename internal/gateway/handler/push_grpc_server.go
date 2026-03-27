package handler

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"

	"pim/internal/config"
	conversationhandler "pim/internal/conversation/handler"
	"pim/internal/conversation/model"
	pbgateway "pim/internal/gateway/pb"
	observemetrics "pim/internal/kit/observability/metrics"
	pbgroup "pim/proto/group/v1"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type PushServiceServer struct {
	pbgateway.UnimplementedPushServiceServer
}

func resolvePushContent(content string, contentBytes []byte) string {
	// fast path: 上游已提供 JSON，直接透传，避免重复 proto->json 转换。
	if content != "" {
		return content
	}
	if len(contentBytes) == 0 {
		return content
	}
	var gp pbgroup.GroupPushMessage
	if err := proto.Unmarshal(contentBytes, &gp); err != nil {
		return content
	}
	data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(&gp)
	if err != nil {
		return content
	}
	return string(data)
}

func resolvePushContentCached(content string, contentBytes []byte, cache map[string]string) string {
	// fast path: 上游已提供 JSON，直接透传，避免把 contentBytes 转 string 作为 key 的额外分配。
	if content != "" {
		return content
	}
	if len(contentBytes) == 0 {
		return content
	}
	key := string(contentBytes)
	if v, ok := cache[key]; ok {
		return v
	}
	v := resolvePushContent(content, contentBytes)
	cache[key] = v
	return v
}

// NewPushServiceServer 创建 Gateway 内部推送 gRPC 服务。
func NewPushServiceServer() *PushServiceServer { return &PushServiceServer{} }

// PushToConn 把消息下行到目标用户的 WebSocket 连接。
func (s *PushServiceServer) PushToConn(ctx context.Context, req *pbgateway.PushToConnRequest) (*pbgateway.PushToConnResponse, error) {
	_ = ctx // 预留 trace/timeout 扩展位，当前直接走本地 ws 推送。
	from := uint(req.GetFromUserId())
	to := uint(req.GetToUserId())
	content := resolvePushContent(req.GetContent(), req.GetContentBytes())
	// PushToUser 只推当前 gateway 进程内连接；跨节点由上游先做路由。
	if err := conversationhandler.PushToUser(to, from, content); err != nil {
		observemetrics.ObserveGatewayPush("error")
		log.Printf("PushToConn: failed to push from=%d to=%d: %v", from, to, err)
		return &pbgateway.PushToConnResponse{Ok: false, Error: err.Error()}, nil
	}
	observemetrics.ObserveGatewayPush("ok")
	return &pbgateway.PushToConnResponse{Ok: true, Error: ""}, nil
}

type payloadCacheKey struct {
	from    uint
	content string
}

// PushBatchToConn 批量下行推送。
// 并发语义：
// - 先按 toUser 分链，确保“同接收者顺序一致”；
// - 不同接收者链路可并行执行（受 GatewayPushBatchParallel 限制）；
// - 同 (from, content) 复用编码结果，减少重复 JSON 编码开销。
func (s *PushServiceServer) PushBatchToConn(ctx context.Context, req *pbgateway.PushBatchToConnRequest) (*pbgateway.PushBatchToConnResponse, error) {
	_ = ctx
	items := req.GetItems()
	if len(items) == 0 {
		return &pbgateway.PushBatchToConnResponse{}, nil
	}

	chains := make(map[uint][]*pbgateway.PushBatchItem)
	for _, it := range items {
		chains[uint(it.GetToUserId())] = append(chains[uint(it.GetToUserId())], it)
	}

	// 跨用户共享：同 (from, content) 只 marshal 一次
	payloadCache := make(map[payloadCacheKey][]byte, 128)
	var payloadMu sync.Mutex

	par := config.GatewayPushBatchParallel
	if par <= 0 {
		par = 32
	}

	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	var okN atomic.Uint32
	var failN atomic.Uint32

	for _, chain := range chains {
		chain := chain
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			contentCache := make(map[string]string, 64)
			for _, it := range chain {
				from := uint(it.GetFromUserId())
				to := uint(it.GetToUserId())
				content := resolvePushContentCached(it.GetContent(), it.GetContentBytes(), contentCache)
				k := payloadCacheKey{from: from, content: content}
				payloadMu.Lock()
				b, ok := payloadCache[k]
				if !ok {
					pm := model.PushMessage{From: from, Content: content}
					b, _ = json.Marshal(pm)
					payloadCache[k] = b
				}
				payloadMu.Unlock()
				if err := conversationhandler.WriteBytesToUser(to, b); err != nil {
					failN.Add(1)
					observemetrics.ObserveGatewayPush("error")
					continue
				}
				okN.Add(1)
				observemetrics.ObserveGatewayPush("ok")
			}
		}()
	}
	wg.Wait()

	return &pbgateway.PushBatchToConnResponse{
		Total:   uint32(len(items)),
		Success: okN.Load(),
		Failed:  failN.Load(),
	}, nil
}
