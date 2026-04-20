package grpcresolver

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc/resolver"

	"pim/internal/config"
	"pim/internal/registry"
)

const Scheme = "pim-etcd"

type pimBuilder struct{}

func (*pimBuilder) Scheme() string { return Scheme }

func (*pimBuilder) Build(target resolver.Target, cc resolver.ClientConn, _ resolver.BuildOptions) (resolver.Resolver, error) {
	path := strings.TrimSpace(target.URL.Path)
	logical := strings.TrimPrefix(path, "/")
	if logical == "" {
		logical = strings.TrimSpace(target.URL.Host)
	}
	if logical == "" {
		return nil, fmt.Errorf("pim-etcd: missing logical service in target %q", target.URL.String())
	}
	cli, err := registry.EtcdClient(context.Background())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &pimResolver{
		client:  cli,
		logical: logical,
		cc:      cc,
		cancel:  cancel,
	}
	go r.run(ctx)
	return r, nil
}

type pimResolver struct {
	client  *clientv3.Client
	logical string
	cc      resolver.ClientConn
	cancel  context.CancelFunc
	mu      sync.Mutex
}

func (r *pimResolver) run(ctx context.Context) {
	prefix := registry.EndpointsPrefix(config.EtcdKeyPrefix, r.logical)
	if err := r.refresh(ctx, prefix); err != nil {
		log.Printf("pim-etcd resolver %s initial refresh: %v", r.logical, err)
	}
	wch := r.client.Watch(ctx, prefix, clientv3.WithPrefix())
	for {
		select {
		case <-ctx.Done():
			return
		case wr, ok := <-wch:
			if !ok {
				return
			}
			if wr.Err() != nil {
				log.Printf("pim-etcd resolver %s watch: %v", r.logical, wr.Err())
				continue
			}
			if err := r.refresh(ctx, prefix); err != nil {
				log.Printf("pim-etcd resolver %s refresh: %v", r.logical, err)
			}
		}
	}
}

func (r *pimResolver) refresh(ctx context.Context, prefix string) error {
	resp, err := r.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return err
	}
	addrs := make([]resolver.Address, 0, len(resp.Kvs))
	seen := make(map[string]struct{})
	for _, kv := range resp.Kvs {
		rec, err := registry.DecodeEndpointRecord(kv.Value)
		if err != nil || rec.Addr == "" {
			continue
		}
		a := strings.TrimSpace(rec.Addr)
		if a == "" {
			continue
		}
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		addrs = append(addrs, resolver.Address{Addr: a})
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cc.UpdateState(resolver.State{Addresses: addrs})
}

func (r *pimResolver) ResolveNow(resolver.ResolveNowOptions) {}

func (r *pimResolver) Close() {
	r.cancel()
}

func init() {
	resolver.Register(&pimBuilder{})
}
