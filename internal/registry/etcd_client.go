package registry

import (
	"context"
	"fmt"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"pim/internal/config"
)

var (
	etcdMu     sync.Mutex
	etcdShared *clientv3.Client
)

// EtcdClient 返回进程内单例 etcd v3 客户端（懒连接）。
func EtcdClient(ctx context.Context) (*clientv3.Client, error) {
	etcdMu.Lock()
	defer etcdMu.Unlock()
	if etcdShared != nil {
		return etcdShared, nil
	}
	eps := config.EtcdEndpointList()
	if len(eps) == 0 {
		return nil, fmt.Errorf("ETCD_ENDPOINTS is empty")
	}
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   eps,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	if _, err := cli.Status(ctx, eps[0]); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("etcd status: %w", err)
	}
	etcdShared = cli
	return etcdShared, nil
}

// CloseEtcd 关闭单例客户端。
func CloseEtcd() {
	etcdMu.Lock()
	defer etcdMu.Unlock()
	if etcdShared != nil {
		_ = etcdShared.Close()
		etcdShared = nil
	}
}
