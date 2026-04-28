// Command pim-prom-etcd-sd：读 etcd 中 observe 服务注册的 HTTP 地址，生成 Prometheus file_sd 的 JSON。
//
// 	go run ./cmd/pim-prom-etcd-sd/ -out deployments/observability/file_sd/observe.json
//
// Docker 内 Prom 需抓宿主机进程时，用 -scrape-host=host.docker.internal 将 127.0.0.1 替换成可解析的主机名。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"pim/internal/config"
	"pim/internal/registry"
)

type fileSDEntry struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

func main() {
	outPath := flag.String("out", "deployments/observability/file_sd/observe.json", "output file path")
	scrapeHost := flag.String("scrape-host", "host.docker.internal", "replace 127.0.0.1 in registered http addr for Prometheus in Docker")
	interval := flag.Duration("interval", 0, "if >0, loop every interval; else one-shot")
	flag.Parse()

	ctx := context.Background()
	do := func() error {
		cli, err := registry.EtcdClient(ctx)
		if err != nil {
			return err
		}

		prefix := registry.EndpointsPrefix(config.EtcdKeyPrefix, registry.LogicalObserve)
		resp, err := cli.Get(ctx, prefix, clientv3.WithPrefix())
		if err != nil {
			return err
		}
		var entries []fileSDEntry
		for _, kv := range resp.Kvs {
			rec, err := registry.DecodeEndpointRecord(kv.Value)
			if err != nil || strings.TrimSpace(rec.Addr) == "" {
				continue
			}
			u, err := url.Parse(rec.Addr)
			if err != nil || u.Host == "" {
				continue
			}
			hostport := u.Host
			if u.Hostname() == "127.0.0.1" && strings.TrimSpace(*scrapeHost) != "" {
				hostport = *scrapeHost
				if p := u.Port(); p != "" {
					hostport = *scrapeHost + ":" + p
				}
			}
			entries = append(entries, fileSDEntry{
				Targets: []string{hostport},
				Labels: map[string]string{
					"job":     "pim-observe",
					"service": "observe-service",
					"node_id": rec.NodeID,
				},
			})
		}
		if len(entries) == 0 {
			log.Println("pim-prom-etcd-sd: no observe endpoints in etcd; writing empty list")
		}
		b, err := json.MarshalIndent(entries, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(*outPath, append(b, '\n'), 0644)
	}

	if *interval <= 0 {
		if err := do(); err != nil {
			log.Fatal(err)
		}
		return
	}
	for {
		if err := do(); err != nil {
			log.Printf("pim-prom-etcd-sd: %v", err)
		} else {
			log.Printf("pim-prom-etcd-sd: wrote %s", *outPath)
		}
		time.Sleep(*interval)
	}
}
