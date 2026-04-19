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
		// Respect explicitly exported env from current shell.
		if os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
}

func loadEnvForAll() {
	// Prefer repo-level .env, fallback to deployments template location.
	tryLoadEnvFile(".env")
	tryLoadEnvFile(filepath.Join("deployments", "docker-compose", ".env"))
}

func newGoRunService(name, path string, extraEnv map[string]string) *service {
	// path 是子服务的 cmd 目录，比如 ./cmd/auth-service
	cmd := exec.Command("go", "run", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = mergeEnv(os.Environ(), extraEnv)
	return &service{
		Name: name,
		Cmd:  cmd,
	}
}

func main() {
	loadEnvForAll()

	// 1) 统一拉起核心服务，便于本地联调（等价于多开终端 go run）。
	// 本地多实例编排：
	// - user/auth/gateway: 双实例
	// - conversation/group: 三实例
	// - friend: 单实例
	// - gateway 两实例分别路由到不同 auth/conversation/group 实例，便于压测分流
	pushTargets := getenvOr("GATEWAY_PUSH_GRPC_TARGETS", "gateway-1=localhost:26090,gateway-2=localhost:26190")
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

	services := []*service{
		newGoRunService("user-service-1", "./cmd/user-service", map[string]string{
			"USER_HTTP_ADDR":                  getenvOr("USER1_HTTP_ADDR", ":26001"),
			"USER_GRPC_ADDR":                  user1GRPC,
			"POSTGRES_MAX_OPEN_CONNS":         getenvOr("USER_POSTGRES_MAX_OPEN_CONNS", "64"),
			"POSTGRES_MAX_IDLE_CONNS":         getenvOr("USER_POSTGRES_MAX_IDLE_CONNS", "32"),
			"POSTGRES_CONN_MAX_LIFETIME_SEC":  getenvOr("POSTGRES_CONN_MAX_LIFETIME_SEC", "300"),
			"POSTGRES_CONN_MAX_IDLE_TIME_SEC": getenvOr("POSTGRES_CONN_MAX_IDLE_TIME_SEC", "120"),
		}),
		newGoRunService("user-service-2", "./cmd/user-service", map[string]string{
			"USER_HTTP_ADDR":                  getenvOr("USER2_HTTP_ADDR", ":26101"),
			"USER_GRPC_ADDR":                  user2GRPC,
			"POSTGRES_MAX_OPEN_CONNS":         getenvOr("USER_POSTGRES_MAX_OPEN_CONNS", "64"),
			"POSTGRES_MAX_IDLE_CONNS":         getenvOr("USER_POSTGRES_MAX_IDLE_CONNS", "32"),
			"POSTGRES_CONN_MAX_LIFETIME_SEC":  getenvOr("POSTGRES_CONN_MAX_LIFETIME_SEC", "300"),
			"POSTGRES_CONN_MAX_IDLE_TIME_SEC": getenvOr("POSTGRES_CONN_MAX_IDLE_TIME_SEC", "120"),
		}),
		newGoRunService("auth-service-1", "./cmd/auth-service", map[string]string{
			"AUTH_HTTP_ADDR":           getenvOr("AUTH1_HTTP_ADDR", ":26000"),
			"AUTH_GRPC_ADDR":           auth1GRPC,
			"USER_SERVICE_GRPC_TARGET": getenvOr("AUTH1_USER_SERVICE_GRPC_TARGET", "localhost"+user1GRPC),
			"POSTGRES_MAX_OPEN_CONNS":  getenvOr("AUTH_POSTGRES_MAX_OPEN_CONNS", "32"),
			"POSTGRES_MAX_IDLE_CONNS":  getenvOr("AUTH_POSTGRES_MAX_IDLE_CONNS", "16"),
		}),
		newGoRunService("auth-service-2", "./cmd/auth-service", map[string]string{
			"AUTH_HTTP_ADDR":           getenvOr("AUTH2_HTTP_ADDR", ":26100"),
			"AUTH_GRPC_ADDR":           auth2GRPC,
			"USER_SERVICE_GRPC_TARGET": getenvOr("AUTH2_USER_SERVICE_GRPC_TARGET", "localhost"+user2GRPC),
			"POSTGRES_MAX_OPEN_CONNS":  getenvOr("AUTH_POSTGRES_MAX_OPEN_CONNS", "32"),
			"POSTGRES_MAX_IDLE_CONNS":  getenvOr("AUTH_POSTGRES_MAX_IDLE_CONNS", "16"),
		}),
		newGoRunService("friend-service-1", "./cmd/friend-service", map[string]string{
			"FRIEND_HTTP_ADDR":        getenvOr("FRIEND1_HTTP_ADDR", ":26002"),
			"FRIEND_GRPC_ADDR":        friend1GRPC,
			"POSTGRES_MAX_OPEN_CONNS": getenvOr("FRIEND_POSTGRES_MAX_OPEN_CONNS", "6"),
			"POSTGRES_MAX_IDLE_CONNS": getenvOr("FRIEND_POSTGRES_MAX_IDLE_CONNS", "3"),
		}),
		newGoRunService("conversation-service-1", "./cmd/conversation-service", map[string]string{
			"CONVERSATION_HTTP_ADDR":    getenvOr("CONVERSATION1_HTTP_ADDR", ":26003"),
			"CONVERSATION_GRPC_ADDR":    conv1GRPC,
			"GATEWAY_PUSH_GRPC_TARGETS": pushTargets,
			"POSTGRES_MAX_OPEN_CONNS":   getenvOr("CONVERSATION_POSTGRES_MAX_OPEN_CONNS", "8"),
			"POSTGRES_MAX_IDLE_CONNS":   getenvOr("CONVERSATION_POSTGRES_MAX_IDLE_CONNS", "4"),
		}),
		newGoRunService("conversation-service-2", "./cmd/conversation-service", map[string]string{
			"CONVERSATION_HTTP_ADDR":    getenvOr("CONVERSATION2_HTTP_ADDR", ":26103"),
			"CONVERSATION_GRPC_ADDR":    conv2GRPC,
			"GATEWAY_PUSH_GRPC_TARGETS": pushTargets,
			"POSTGRES_MAX_OPEN_CONNS":   getenvOr("CONVERSATION_POSTGRES_MAX_OPEN_CONNS", "8"),
			"POSTGRES_MAX_IDLE_CONNS":   getenvOr("CONVERSATION_POSTGRES_MAX_IDLE_CONNS", "4"),
		}),
		newGoRunService("conversation-service-3", "./cmd/conversation-service", map[string]string{
			"CONVERSATION_HTTP_ADDR":    getenvOr("CONVERSATION3_HTTP_ADDR", ":26203"),
			"CONVERSATION_GRPC_ADDR":    conv3GRPC,
			"GATEWAY_PUSH_GRPC_TARGETS": pushTargets,
			"POSTGRES_MAX_OPEN_CONNS":   getenvOr("CONVERSATION_POSTGRES_MAX_OPEN_CONNS", "8"),
			"POSTGRES_MAX_IDLE_CONNS":   getenvOr("CONVERSATION_POSTGRES_MAX_IDLE_CONNS", "4"),
		}),
		newGoRunService("group-service-1", "./cmd/group-service", map[string]string{
			"GROUP_HTTP_ADDR":           getenvOr("GROUP1_HTTP_ADDR", ":26004"),
			"GROUP_GRPC_ADDR":           group1GRPC,
			"GATEWAY_PUSH_GRPC_TARGETS": pushTargets,
			"POSTGRES_MAX_OPEN_CONNS":   getenvOr("GROUP_POSTGRES_MAX_OPEN_CONNS", "8"),
			"POSTGRES_MAX_IDLE_CONNS":   getenvOr("GROUP_POSTGRES_MAX_IDLE_CONNS", "4"),
		}),
		newGoRunService("group-service-2", "./cmd/group-service", map[string]string{
			"GROUP_HTTP_ADDR":           getenvOr("GROUP2_HTTP_ADDR", ":26104"),
			"GROUP_GRPC_ADDR":           group2GRPC,
			"GATEWAY_PUSH_GRPC_TARGETS": pushTargets,
			"POSTGRES_MAX_OPEN_CONNS":   getenvOr("GROUP_POSTGRES_MAX_OPEN_CONNS", "8"),
			"POSTGRES_MAX_IDLE_CONNS":   getenvOr("GROUP_POSTGRES_MAX_IDLE_CONNS", "4"),
		}),
		newGoRunService("group-service-3", "./cmd/group-service", map[string]string{
			"GROUP_HTTP_ADDR":           getenvOr("GROUP3_HTTP_ADDR", ":26204"),
			"GROUP_GRPC_ADDR":           group3GRPC,
			"GATEWAY_PUSH_GRPC_TARGETS": pushTargets,
			"POSTGRES_MAX_OPEN_CONNS":   getenvOr("GROUP_POSTGRES_MAX_OPEN_CONNS", "8"),
			"POSTGRES_MAX_IDLE_CONNS":   getenvOr("GROUP_POSTGRES_MAX_IDLE_CONNS", "4"),
		}),
		newGoRunService("file-service-1", "./cmd/file-service", map[string]string{
			"FILE_HTTP_ADDR":             getenvOr("FILE1_HTTP_ADDR", ":26006"),
			"FILE_GRPC_ADDR":             file1GRPC,
			"GROUP_SERVICE_GRPC_TARGET":  getenvOr("FILE1_GROUP_SERVICE_GRPC_TARGET", "localhost"+group1GRPC),
			"FRIEND_SERVICE_GRPC_TARGET": getenvOr("FILE1_FRIEND_SERVICE_GRPC_TARGET", "localhost"+friend1GRPC),
			"POSTGRES_MAX_OPEN_CONNS":    getenvOr("FILE_POSTGRES_MAX_OPEN_CONNS", "10"),
			"POSTGRES_MAX_IDLE_CONNS":    getenvOr("FILE_POSTGRES_MAX_IDLE_CONNS", "5"),
			"FILE_SERVICE_HTTP_URL":      "http://localhost" + getenvOr("FILE1_HTTP_ADDR", ":26006"),
		}),
		newGoRunService("log-service-1", "./cmd/log-service", map[string]string{
			"LOG_HTTP_ADDR":     log1HTTP,
			"ELASTICSEARCH_URL": getenvOr("ELASTICSEARCH_URL", "http://localhost:9200"),
		}),
		newGoRunService("gateway-1", "./cmd/gateway", map[string]string{
			"GATEWAY_HTTP_ADDR":                getenvOr("GATEWAY1_HTTP_ADDR", ":26080"),
			"GATEWAY_PUSH_GRPC_ADDR":           getenvOr("GATEWAY1_PUSH_GRPC_ADDR", ":26090"),
			"GATEWAY_NODE_ID":                  getenvOr("GATEWAY1_NODE_ID", "gateway-1"),
			"AUTH_SERVICE_GRPC_TARGET":         getenvOr("GATEWAY1_AUTH_SERVICE_GRPC_TARGET", "localhost"+auth1GRPC),
			"USER_SERVICE_GRPC_TARGET":         getenvOr("GATEWAY1_USER_SERVICE_GRPC_TARGET", "localhost"+user1GRPC),
			"FRIEND_SERVICE_GRPC_TARGET":       getenvOr("GATEWAY1_FRIEND_SERVICE_GRPC_TARGET", "localhost"+friend1GRPC),
			"CONVERSATION_SERVICE_GRPC_TARGET": getenvOr("GATEWAY1_CONVERSATION_SERVICE_GRPC_TARGET", "localhost"+conv1GRPC),
			"GROUP_SERVICE_GRPC_TARGET":        getenvOr("GATEWAY1_GROUP_SERVICE_GRPC_TARGET", "localhost"+group1GRPC),
			"FILE_SERVICE_GRPC_TARGET":         getenvOr("GATEWAY1_FILE_SERVICE_GRPC_TARGET", "localhost"+file1GRPC),
			"LOG_SERVICE_HTTP_URL":             getenvOr("LOG1_HTTP_URL", "http://localhost"+log1HTTP),
			"FILE_SERVICE_HTTP_URL":            getenvOr("FILE1_HTTP_URL", "http://localhost"+getenvOr("FILE1_HTTP_ADDR", ":26006")),
		}),
		newGoRunService("gateway-2", "./cmd/gateway", map[string]string{
			"GATEWAY_HTTP_ADDR":                getenvOr("GATEWAY2_HTTP_ADDR", ":26180"),
			"GATEWAY_PUSH_GRPC_ADDR":           getenvOr("GATEWAY2_PUSH_GRPC_ADDR", ":26190"),
			"GATEWAY_NODE_ID":                  getenvOr("GATEWAY2_NODE_ID", "gateway-2"),
			"AUTH_SERVICE_GRPC_TARGET":         getenvOr("GATEWAY2_AUTH_SERVICE_GRPC_TARGET", "localhost"+auth2GRPC),
			"USER_SERVICE_GRPC_TARGET":         getenvOr("GATEWAY2_USER_SERVICE_GRPC_TARGET", "localhost"+user2GRPC),
			"FRIEND_SERVICE_GRPC_TARGET":       getenvOr("GATEWAY2_FRIEND_SERVICE_GRPC_TARGET", "localhost"+friend1GRPC),
			"CONVERSATION_SERVICE_GRPC_TARGET": getenvOr("GATEWAY2_CONVERSATION_SERVICE_GRPC_TARGET", "localhost"+conv3GRPC),
			"GROUP_SERVICE_GRPC_TARGET":        getenvOr("GATEWAY2_GROUP_SERVICE_GRPC_TARGET", "localhost"+group3GRPC),
			"FILE_SERVICE_GRPC_TARGET":         getenvOr("GATEWAY2_FILE_SERVICE_GRPC_TARGET", "localhost"+file1GRPC),
			"LOG_SERVICE_HTTP_URL":             getenvOr("LOG1_HTTP_URL", "http://localhost"+log1HTTP),
			"FILE_SERVICE_HTTP_URL":            getenvOr("FILE1_HTTP_URL", "http://localhost"+getenvOr("FILE1_HTTP_ADDR", ":26006")),
		}),
	}

	// 2) 启动所有子服务；任何一个启动失败直接退出，避免半可用状态。
	for _, s := range services {
		log.Printf("starting %s ...", s.Name)
		if err := s.Cmd.Start(); err != nil {
			log.Fatalf("failed to start %s: %v", s.Name, err)
		}
	}

	log.Println("all services started")

	// 3) 监听 Ctrl+C，退出时尽量回收子进程，防止端口残留占用。
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down all services...")
	for _, s := range services {
		if s.Cmd.Process == nil {
			continue
		}
		if runtime.GOOS == "windows" {
			// Windows 下优先使用 Kill，避免信号语义差异导致子进程残留。
			_ = s.Cmd.Process.Kill()
		} else {
			// Linux/macOS 使用 SIGTERM，给子进程留清理机会。
			_ = s.Cmd.Process.Signal(syscall.SIGTERM)
		}
	}
}
