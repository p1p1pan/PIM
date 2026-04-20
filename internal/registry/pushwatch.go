package registry

import (
	"context"
	"log"
	"strings"
	"sync"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"pim/internal/config"
	pbgateway "pim/internal/gateway/pb"
)

// GatewayPushWatcher Watch etcd 中 gateway-push 实例并维护 node_id -> PushServiceClient。
type GatewayPushWatcher struct {
	mu      sync.RWMutex
	clients map[string]pbgateway.PushServiceClient
	conns   map[string]*grpc.ClientConn
	addrs   map[string]string

	cancel context.CancelFunc
}

// StartGatewayPushWatcher 启动后台 Watch；关闭时调用 Close。
func StartGatewayPushWatcher(ctx context.Context, cli *clientv3.Client) (*GatewayPushWatcher, error) {
	wctx, cancel := context.WithCancel(ctx)
	w := &GatewayPushWatcher{
		clients: make(map[string]pbgateway.PushServiceClient),
		conns:   make(map[string]*grpc.ClientConn),
		addrs:   make(map[string]string),
		cancel:  cancel,
	}
	prefix := EndpointsPrefix(config.EtcdKeyPrefix, LogicalGatewayPush)
	if err := w.rebuild(wctx, cli, prefix); err != nil {
		cancel()
		return nil, err
	}
	go w.run(wctx, cli, prefix)
	return w, nil
}

func (w *GatewayPushWatcher) Snapshot() map[string]pbgateway.PushServiceClient {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make(map[string]pbgateway.PushServiceClient, len(w.clients))
	for k, v := range w.clients {
		out[k] = v
	}
	return out
}

func (w *GatewayPushWatcher) Close() {
	w.cancel()
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range w.conns {
		_ = c.Close()
	}
	w.conns = make(map[string]*grpc.ClientConn)
	w.clients = make(map[string]pbgateway.PushServiceClient)
	w.addrs = make(map[string]string)
}

func (w *GatewayPushWatcher) run(ctx context.Context, cli *clientv3.Client, prefix string) {
	watch := cli.Watch(ctx, prefix, clientv3.WithPrefix())
	for {
		select {
		case <-ctx.Done():
			return
		case wr, ok := <-watch:
			if !ok {
				return
			}
			if wr.Err() != nil {
				log.Printf("registry: gateway-push watch error: %v", wr.Err())
				continue
			}
			if err := w.rebuild(ctx, cli, prefix); err != nil {
				log.Printf("registry: gateway-push rebuild: %v", err)
			}
		}
	}
}

func (w *GatewayPushWatcher) rebuild(ctx context.Context, cli *clientv3.Client, prefix string) error {
	resp, err := cli.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return err
	}
	want := make(map[string]string)
	for _, kv := range resp.Kvs {
		rec, err := DecodeEndpointRecord(kv.Value)
		if err != nil || rec.Addr == "" {
			continue
		}
		node := strings.TrimSpace(rec.NodeID)
		if node == "" {
			node = lastPathKey(string(kv.Key))
		}
		if node == "" {
			continue
		}
		want[node] = rec.Addr
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	for node, conn := range w.conns {
		addr, ok := want[node]
		if !ok || addr != w.addrs[node] {
			_ = conn.Close()
			delete(w.conns, node)
			delete(w.clients, node)
			delete(w.addrs, node)
		}
	}
	for node, addr := range want {
		if w.addrs[node] == addr && w.conns[node] != nil {
			continue
		}
		if old := w.conns[node]; old != nil {
			_ = old.Close()
			delete(w.conns, node)
			delete(w.clients, node)
		}
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Printf("registry: dial gateway-push node=%s addr=%s: %v", node, addr, err)
			continue
		}
		w.conns[node] = conn
		w.clients[node] = pbgateway.NewPushServiceClient(conn)
		w.addrs[node] = addr
	}
	return nil
}

func lastPathKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if i := strings.LastIndex(key, "/"); i >= 0 && i+1 < len(key) {
		return key[i+1:]
	}
	return key
}
