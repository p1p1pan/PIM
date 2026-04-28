package main

import (
	"bufio"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

type service struct {
	Name string
	Cmd  *exec.Cmd
}

func mergeEnv(base []string, extra map[string]string) []string {
	merged := map[string]string{}
	for _, kv := range base {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			continue
		}
		merged[parts[0]] = parts[1]
	}
	for k, v := range extra {
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}

func combine(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func getenvOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func tryLoadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		if k == "" {
			continue
		}
		if os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
}

func loadEnvForAll() {
	tryLoadEnvFile(".env")
	tryLoadEnvFile(filepath.Join("deployments", "docker-compose", ".env"))
}

func newGoRunService(name, path string, extraEnv map[string]string) *service {
	cmd := exec.Command("go", "run", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = mergeEnv(os.Environ(), extraEnv)
	return &service{
		Name: name,
		Cmd:  cmd,
	}
}

// localGRPCAdvertise 将 ":port" 转为宿主机可达地址（与 config.EffectiveAdvertise 一致）。
func localGRPCAdvertise(portSpec string) string {
	p := strings.TrimSpace(portSpec)
	if strings.HasPrefix(p, ":") {
		return "127.0.0.1" + p
	}
	return p
}

func main() {
	loadEnvForAll()

	common := map[string]string{
		"ETCD_ENDPOINTS":  getenvOr("ETCD_ENDPOINTS", "127.0.0.1:2379"),
		"ETCD_KEY_PREFIX": getenvOr("ETCD_KEY_PREFIX", "/pim/v1"),
	}

	user1GRPC := getenvOr("USER1_GRPC_ADDR", ":26011")
	user2GRPC := getenvOr("USER2_GRPC_ADDR", ":26111")
	auth1GRPC := getenvOr("AUTH1_GRPC_ADDR", ":26005")
	auth2GRPC := getenvOr("AUTH2_GRPC_ADDR", ":26105")
	friend1GRPC := getenvOr("FRIEND1_GRPC_ADDR", ":26012")
	conv1GRPC := getenvOr("CONVERSATION1_GRPC_ADDR", ":26013")
	conv2GRPC := getenvOr("CONVERSATION2_GRPC_ADDR", ":26113")
	conv3GRPC := getenvOr("CONVERSATION3_GRPC_ADDR", ":26213")
	group1GRPC := getenvOr("GROUP1_GRPC_ADDR", ":26014")
	group2GRPC := getenvOr("GROUP2_GRPC_ADDR", ":26114")
	group3GRPC := getenvOr("GROUP3_GRPC_ADDR", ":26214")
	file1GRPC := getenvOr("FILE1_GRPC_ADDR", ":26015")
	log1HTTP := getenvOr("LOG1_HTTP_ADDR", ":26016")

	user1 := newGoRunService("user-service-1", "./cmd/user-service", combine(common, map[string]string{
		"USER_HTTP_ADDR":                  getenvOr("USER1_HTTP_ADDR", ":26001"),
		"USER_GRPC_ADDR":                  user1GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR":     localGRPCAdvertise(user1GRPC),
		"SERVICE_INSTANCE_ID":             "user-service-1",
		"POSTGRES_MAX_OPEN_CONNS":         getenvOr("USER_POSTGRES_MAX_OPEN_CONNS", "64"),
		"POSTGRES_MAX_IDLE_CONNS":         getenvOr("USER_POSTGRES_MAX_IDLE_CONNS", "32"),
		"POSTGRES_CONN_MAX_LIFETIME_SEC":  getenvOr("POSTGRES_CONN_MAX_LIFETIME_SEC", "300"),
		"POSTGRES_CONN_MAX_IDLE_TIME_SEC": getenvOr("POSTGRES_CONN_MAX_IDLE_TIME_SEC", "120"),
	}))
	user2 := newGoRunService("user-service-2", "./cmd/user-service", combine(common, map[string]string{
		"USER_HTTP_ADDR":                  getenvOr("USER2_HTTP_ADDR", ":26101"),
		"USER_GRPC_ADDR":                  user2GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR":     localGRPCAdvertise(user2GRPC),
		"SERVICE_INSTANCE_ID":             "user-service-2",
		"POSTGRES_MAX_OPEN_CONNS":         getenvOr("USER_POSTGRES_MAX_OPEN_CONNS", "64"),
		"POSTGRES_MAX_IDLE_CONNS":         getenvOr("USER_POSTGRES_MAX_IDLE_CONNS", "32"),
		"POSTGRES_CONN_MAX_LIFETIME_SEC":  getenvOr("POSTGRES_CONN_MAX_LIFETIME_SEC", "300"),
		"POSTGRES_CONN_MAX_IDLE_TIME_SEC": getenvOr("POSTGRES_CONN_MAX_IDLE_TIME_SEC", "120"),
	}))
	friend1 := newGoRunService("friend-service-1", "./cmd/friend-service", combine(common, map[string]string{
		"FRIEND_HTTP_ADDR":            getenvOr("FRIEND1_HTTP_ADDR", ":26002"),
		"FRIEND_GRPC_ADDR":            friend1GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(friend1GRPC),
		"SERVICE_INSTANCE_ID":         "friend-service-1",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("FRIEND_POSTGRES_MAX_OPEN_CONNS", "6"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("FRIEND_POSTGRES_MAX_IDLE_CONNS", "3"),
	}))

	auth1 := newGoRunService("auth-service-1", "./cmd/auth-service", combine(common, map[string]string{
		"AUTH_HTTP_ADDR":              getenvOr("AUTH1_HTTP_ADDR", ":26000"),
		"AUTH_GRPC_ADDR":              auth1GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(auth1GRPC),
		"SERVICE_INSTANCE_ID":         "auth-service-1",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("AUTH_POSTGRES_MAX_OPEN_CONNS", "32"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("AUTH_POSTGRES_MAX_IDLE_CONNS", "16"),
	}))
	auth2 := newGoRunService("auth-service-2", "./cmd/auth-service", combine(common, map[string]string{
		"AUTH_HTTP_ADDR":              getenvOr("AUTH2_HTTP_ADDR", ":26100"),
		"AUTH_GRPC_ADDR":              auth2GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(auth2GRPC),
		"SERVICE_INSTANCE_ID":         "auth-service-2",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("AUTH_POSTGRES_MAX_OPEN_CONNS", "32"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("AUTH_POSTGRES_MAX_IDLE_CONNS", "16"),
	}))
	file1 := newGoRunService("file-service-1", "./cmd/file-service", combine(common, map[string]string{
		"FILE_HTTP_ADDR":              getenvOr("FILE1_HTTP_ADDR", ":26006"),
		"FILE_GRPC_ADDR":              file1GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(file1GRPC),
		"SERVICE_INSTANCE_ID":         "file-service-1",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("FILE_POSTGRES_MAX_OPEN_CONNS", "10"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("FILE_POSTGRES_MAX_IDLE_CONNS", "5"),
		"FILE_SERVICE_HTTP_URL":       "http://localhost" + getenvOr("FILE1_HTTP_ADDR", ":26006"),
	}))

	conv1 := newGoRunService("conversation-service-1", "./cmd/conversation-service", combine(common, map[string]string{
		"CONVERSATION_HTTP_ADDR":      getenvOr("CONVERSATION1_HTTP_ADDR", ":26003"),
		"CONVERSATION_GRPC_ADDR":      conv1GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(conv1GRPC),
		"SERVICE_INSTANCE_ID":         "conversation-service-1",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("CONVERSATION_POSTGRES_MAX_OPEN_CONNS", "8"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("CONVERSATION_POSTGRES_MAX_IDLE_CONNS", "4"),
	}))
	conv2 := newGoRunService("conversation-service-2", "./cmd/conversation-service", combine(common, map[string]string{
		"CONVERSATION_HTTP_ADDR":      getenvOr("CONVERSATION2_HTTP_ADDR", ":26103"),
		"CONVERSATION_GRPC_ADDR":      conv2GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(conv2GRPC),
		"SERVICE_INSTANCE_ID":         "conversation-service-2",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("CONVERSATION_POSTGRES_MAX_OPEN_CONNS", "8"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("CONVERSATION_POSTGRES_MAX_IDLE_CONNS", "4"),
	}))
	conv3 := newGoRunService("conversation-service-3", "./cmd/conversation-service", combine(common, map[string]string{
		"CONVERSATION_HTTP_ADDR":      getenvOr("CONVERSATION3_HTTP_ADDR", ":26203"),
		"CONVERSATION_GRPC_ADDR":      conv3GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(conv3GRPC),
		"SERVICE_INSTANCE_ID":         "conversation-service-3",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("CONVERSATION_POSTGRES_MAX_OPEN_CONNS", "8"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("CONVERSATION_POSTGRES_MAX_IDLE_CONNS", "4"),
	}))
	group1 := newGoRunService("group-service-1", "./cmd/group-service", combine(common, map[string]string{
		"GROUP_HTTP_ADDR":             getenvOr("GROUP1_HTTP_ADDR", ":26004"),
		"GROUP_GRPC_ADDR":             group1GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(group1GRPC),
		"SERVICE_INSTANCE_ID":         "group-service-1",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("GROUP_POSTGRES_MAX_OPEN_CONNS", "8"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("GROUP_POSTGRES_MAX_IDLE_CONNS", "4"),
	}))
	group2 := newGoRunService("group-service-2", "./cmd/group-service", combine(common, map[string]string{
		"GROUP_HTTP_ADDR":             getenvOr("GROUP2_HTTP_ADDR", ":26104"),
		"GROUP_GRPC_ADDR":             group2GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(group2GRPC),
		"SERVICE_INSTANCE_ID":         "group-service-2",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("GROUP_POSTGRES_MAX_OPEN_CONNS", "8"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("GROUP_POSTGRES_MAX_IDLE_CONNS", "4"),
	}))
	group3 := newGoRunService("group-service-3", "./cmd/group-service", combine(common, map[string]string{
		"GROUP_HTTP_ADDR":             getenvOr("GROUP3_HTTP_ADDR", ":26204"),
		"GROUP_GRPC_ADDR":             group3GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(group3GRPC),
		"SERVICE_INSTANCE_ID":         "group-service-3",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("GROUP_POSTGRES_MAX_OPEN_CONNS", "8"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("GROUP_POSTGRES_MAX_IDLE_CONNS", "4"),
	}))
	log1 := newGoRunService("log-service-1", "./cmd/log-service", combine(common, map[string]string{
		"LOG_HTTP_ADDR":     log1HTTP,
		"ELASTICSEARCH_URL": getenvOr("ELASTICSEARCH_URL", "http://localhost:9200"),
	}))

	observeHTTP := getenvOr("OBSERVE1_HTTP_ADDR", ":26280")
	observe1 := newGoRunService("observe-service-1", "./cmd/observe-service", combine(common, map[string]string{
		"OBSERVE_HTTP_ADDR":         observeHTTP,
		"SERVICE_ADVERTISE_HTTP_ADDR": getenvOr("OBSERVE1_SERVICE_ADVERTISE_HTTP_ADDR", "http://127.0.0.1"+observeHTTP),
		"SERVICE_INSTANCE_ID":       getenvOr("OBSERVE1_SERVICE_INSTANCE_ID", "observe-service-1"),
		"LOG_SERVICE_HTTP_URL":      getenvOr("LOG1_HTTP_URL", "http://localhost"+log1HTTP),
		"FILE_SERVICE_HTTP_URL":     getenvOr("FILE1_HTTP_URL", "http://localhost"+getenvOr("FILE1_HTTP_ADDR", ":26006")),
		"PROMETHEUS_QUERY_URL":      getenvOr("PROMETHEUS_QUERY_URL", "http://localhost:9090"),
		"OBSERVE_GATEWAY_METRICS_SCRAPE_URL": getenvOr("OBSERVE1_GATEWAY_METRICS_SCRAPE_URL", "http://127.0.0.1:26080/metrics"),
	}))

	gw1 := newGoRunService("gateway-1", "./cmd/gateway", combine(common, map[string]string{
		"GATEWAY_HTTP_ADDR":           getenvOr("GATEWAY1_HTTP_ADDR", ":26080"),
		"GATEWAY_PUSH_GRPC_ADDR":      getenvOr("GATEWAY1_PUSH_GRPC_ADDR", ":26090"),
		"GATEWAY_NODE_ID":             getenvOr("GATEWAY1_NODE_ID", "gateway-1"),
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(getenvOr("GATEWAY1_PUSH_GRPC_ADDR", ":26090")),
		"SERVICE_INSTANCE_ID":         "gateway-1",
		"LOG_SERVICE_HTTP_URL":        getenvOr("LOG1_HTTP_URL", "http://localhost"+log1HTTP),
		"FILE_SERVICE_HTTP_URL":       getenvOr("FILE1_HTTP_URL", "http://localhost"+getenvOr("FILE1_HTTP_ADDR", ":26006")),
	}))
	gw2 := newGoRunService("gateway-2", "./cmd/gateway", combine(common, map[string]string{
		"GATEWAY_HTTP_ADDR":           getenvOr("GATEWAY2_HTTP_ADDR", ":26180"),
		"GATEWAY_PUSH_GRPC_ADDR":      getenvOr("GATEWAY2_PUSH_GRPC_ADDR", ":26190"),
		"GATEWAY_NODE_ID":             getenvOr("GATEWAY2_NODE_ID", "gateway-2"),
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(getenvOr("GATEWAY2_PUSH_GRPC_ADDR", ":26190")),
		"SERVICE_INSTANCE_ID":         "gateway-2",
		"LOG_SERVICE_HTTP_URL":        getenvOr("LOG1_HTTP_URL", "http://localhost"+log1HTTP),
		"FILE_SERVICE_HTTP_URL":       getenvOr("FILE1_HTTP_URL", "http://localhost"+getenvOr("FILE1_HTTP_ADDR", ":26006")),
	}))

	waves := [][]*service{
		{user1, user2, friend1},
		{auth1, auth2, file1},
		{conv1, conv2, conv3, group1, group2, group3, log1, observe1},
		{gw1, gw2},
	}
	var services []*service
	for wi, wave := range waves {
		for _, s := range wave {
			services = append(services, s)
			log.Printf("starting %s ...", s.Name)
			if err := s.Cmd.Start(); err != nil {
				log.Fatalf("failed to start %s: %v", s.Name, err)
			}
		}
		if wi+1 < len(waves) {
			time.Sleep(900 * time.Millisecond)
		}
	}

	log.Println("all services started")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down all services...")
	for _, s := range services {
		if s.Cmd.Process == nil {
			continue
		}
		if runtime.GOOS == "windows" {
			_ = s.Cmd.Process.Kill()
		} else {
			_ = s.Cmd.Process.Signal(syscall.SIGTERM)
		}
	}
}
