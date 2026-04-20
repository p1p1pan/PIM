package registry

import (
	"context"
	"fmt"
	"log"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"pim/internal/config"
)

// RegisterEndpoint 使用租约注册实例；返回 revoke 关闭函数。
func RegisterEndpoint(ctx context.Context, cli *clientv3.Client, logical, instanceID string, rec EndpointRecord) (func(), error) {
	if instanceID == "" {
		return nil, fmt.Errorf("empty instance id for %s", logical)
	}
	if rec.Addr == "" {
		return nil, fmt.Errorf("empty addr for %s", logical)
	}
	payload, err := encodeRecord(rec)
	if err != nil {
		return nil, err
	}
	key := endpointInstanceKey(config.EtcdKeyPrefix, logical, instanceID)

	ttl := config.EtcdLeaseTTLSec
	if ttl <= 0 {
		ttl = 10
	}
	lease, err := cli.Grant(ctx, int64(ttl))
	if err != nil {
		return nil, fmt.Errorf("etcd grant: %w", err)
	}
	kaCtx, kaCancel := context.WithCancel(context.Background())
	kaCh, err := cli.KeepAlive(kaCtx, lease.ID)
	if err != nil {
		kaCancel()
		_, _ = cli.Revoke(context.Background(), lease.ID)
		return nil, fmt.Errorf("etcd keepalive: %w", err)
	}
	go func() {
		for range kaCh {
		}
	}()

	if _, err := cli.Put(ctx, key, string(payload), clientv3.WithLease(lease.ID)); err != nil {
		kaCancel()
		_, _ = cli.Revoke(context.Background(), lease.ID)
		return nil, fmt.Errorf("etcd put %s: %w", key, err)
	}
	log.Printf("registry: registered %s instance=%s addr=%s", logical, instanceID, rec.Addr)

	closer := func() {
		kaCancel()
		ctx2, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := cli.Revoke(ctx2, lease.ID); err != nil {
			log.Printf("registry: revoke lease for %s: %v", key, err)
		}
	}
	return closer, nil
}
