package grpcresolver

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

// Dial 通过 pim-etcd resolver 连接逻辑服务，启用 round_robin，并等待进入 Ready。
func Dial(ctx context.Context, logical string) (*grpc.ClientConn, error) {
	target := fmt.Sprintf("%s:///%s", Scheme, logical)
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(`{"loadBalancingConfig":[{"round_robin":{}}]}`),
	)
	if err != nil {
		return nil, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := waitUntilReady(waitCtx, conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("dial %s: %w", logical, err)
	}
	return conn, nil
}

func waitUntilReady(ctx context.Context, conn *grpc.ClientConn) error {
	conn.Connect()
	for {
		s := conn.GetState()
		switch s {
		case connectivity.Ready:
			return nil
		case connectivity.Shutdown:
			return fmt.Errorf("connection shutdown")
		}
		if !conn.WaitForStateChange(ctx, s) {
			return fmt.Errorf("%w", ctx.Err())
		}
	}
}
