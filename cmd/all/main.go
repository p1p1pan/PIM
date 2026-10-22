package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"pim/internal/platform/kit/termcolor"
)

const (
	ansiReset  = "\033[0m"
	ansiGreen  = "\033[32m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
)

type service struct {
	Name      string
	Cmd       *exec.Cmd
	HealthURL string

	mu      sync.Mutex
	ready   bool
	exited  bool
	exitErr error
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

func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("PIM_ANSI") == "1" {
		return true
	}
	return runtime.GOOS != "windows"
}

func colorPrintf(color, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if colorEnabled() {
		fmt.Printf("%s%s%s\n", color, msg, ansiReset)
		return
	}
	fmt.Println(msg)
}

func healthURL(httpAddr, path string) string {
	addr := strings.TrimSpace(httpAddr)
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	if !strings.HasPrefix(addr, "http://") && !strings.HasPrefix(addr, "https://") {
		addr = "http://" + addr
	}
	return strings.TrimRight(addr, "/") + path
}

func localGRPCAdvertise(portSpec string) string {
	p := strings.TrimSpace(portSpec)
	if strings.HasPrefix(p, ":") {
		return "127.0.0.1" + p
	}
	return p
}

func newGoRunService(name, path, health string, extraEnv map[string]string) *service {
	cmd := exec.Command("go", "run", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = mergeEnv(os.Environ(), extraEnv)
	return &service{
		Name:      name,
		Cmd:       cmd,
		HealthURL: health,
	}
}

func (s *service) markReady() {
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
}

func (s *service) markExited(err error) {
	s.mu.Lock()
	s.exited = true
	s.exitErr = err
	s.mu.Unlock()
}

func (s *service) isReady() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

func (s *service) hasExited() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited, s.exitErr
}

func checkHTTPReady(url string) bool {
	if strings.TrimSpace(url) == "" {
		return true
	}
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func waitWaveReady(wave []*service, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		allReady := true
		for _, s := range wave {
			if exited, err := s.hasExited(); exited {
				if err != nil {
					return fmt.Errorf("%s exited: %w", s.Name, err)
				}
				return fmt.Errorf("%s exited unexpectedly", s.Name)
			}
			if s.isReady() {
				continue
			}
			if checkHTTPReady(s.HealthURL) {
				s.markReady()
				colorPrintf(ansiGreen, "[ready] %s", s.Name)
				continue
			}
			allReady = false
		}
		if allReady {
			return nil
		}
		if time.Now().After(deadline) {
			var pending []string
			for _, s := range wave {
				if !s.isReady() {
					pending = append(pending, s.Name)
				}
			}
			return fmt.Errorf("timeout waiting ready: %s", strings.Join(pending, ", "))
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func startWave(wave []*service) error {
	for _, s := range wave {
		colorPrintf(ansiYellow, "[start] %s", s.Name)
		if err := s.Cmd.Start(); err != nil {
			return fmt.Errorf("failed to start %s: %w", s.Name, err)
		}
		go func(svc *service) {
			err := svc.Cmd.Wait()
			svc.markExited(err)
			if err != nil {
				colorPrintf(ansiRed, "[exit] %s: %v", svc.Name, err)
			} else {
				colorPrintf(ansiRed, "[exit] %s", svc.Name)
			}
		}(s)
	}
	return nil
}

func printDemoLinks() {
	lb := getenvOr("PIM_LB_URL", "http://localhost:28080")
	lb = strings.TrimRight(lb, "/")
	colorPrintf(ansiCyan, "-------- open in browser --------")
	colorPrintf(ansiCyan, "  home:     %s/", lb)
	colorPrintf(ansiCyan, "  login:    %s/pages/login.html", lb)
	colorPrintf(ansiCyan, "  register: %s/pages/register.html", lb)
	colorPrintf(ansiCyan, "  im:       %s/pages/im.html", lb)
	colorPrintf(ansiCyan, "  admin:    %s/pages/admin.html", lb)
	colorPrintf(ansiCyan, "--------------------------------")
}

func main() {
	termcolor.EnableWindowsANSI()
	loadEnvForAll()

	common := map[string]string{
		"ETCD_ENDPOINTS":  getenvOr("ETCD_ENDPOINTS", "127.0.0.1:2379"),
		"ETCD_KEY_PREFIX": getenvOr("ETCD_KEY_PREFIX", "/pim/v1"),
	}

	user1HTTP := getenvOr("USER1_HTTP_ADDR", ":26001")
	user2HTTP := getenvOr("USER2_HTTP_ADDR", ":26101")
	user1GRPC := getenvOr("USER1_GRPC_ADDR", ":26011")
	user2GRPC := getenvOr("USER2_GRPC_ADDR", ":26111")
	auth1HTTP := getenvOr("AUTH1_HTTP_ADDR", ":26000")
	auth2HTTP := getenvOr("AUTH2_HTTP_ADDR", ":26100")
	auth1GRPC := getenvOr("AUTH1_GRPC_ADDR", ":26005")
	auth2GRPC := getenvOr("AUTH2_GRPC_ADDR", ":26105")
	friend1HTTP := getenvOr("FRIEND1_HTTP_ADDR", ":26002")
	friend1GRPC := getenvOr("FRIEND1_GRPC_ADDR", ":26012")
	conv1HTTP := getenvOr("CONVERSATION1_HTTP_ADDR", ":26003")
	conv2HTTP := getenvOr("CONVERSATION2_HTTP_ADDR", ":26103")
	conv3HTTP := getenvOr("CONVERSATION3_HTTP_ADDR", ":26203")
	conv1GRPC := getenvOr("CONVERSATION1_GRPC_ADDR", ":26013")
	conv2GRPC := getenvOr("CONVERSATION2_GRPC_ADDR", ":26113")
	conv3GRPC := getenvOr("CONVERSATION3_GRPC_ADDR", ":26213")
	group1HTTP := getenvOr("GROUP1_HTTP_ADDR", ":26004")
	group2HTTP := getenvOr("GROUP2_HTTP_ADDR", ":26104")
	group3HTTP := getenvOr("GROUP3_HTTP_ADDR", ":26204")
	group1GRPC := getenvOr("GROUP1_GRPC_ADDR", ":26014")
	group2GRPC := getenvOr("GROUP2_GRPC_ADDR", ":26114")
	group3GRPC := getenvOr("GROUP3_GRPC_ADDR", ":26214")
	file1HTTP := getenvOr("FILE1_HTTP_ADDR", ":26006")
	file1GRPC := getenvOr("FILE1_GRPC_ADDR", ":26015")
	log1HTTP := getenvOr("LOG1_HTTP_ADDR", ":26016")
	observeHTTP := getenvOr("OBSERVE1_HTTP_ADDR", ":26280")
	gw1HTTP := getenvOr("GATEWAY1_HTTP_ADDR", ":26080")
	gw2HTTP := getenvOr("GATEWAY2_HTTP_ADDR", ":26180")

	user1 := newGoRunService("user-service-1", "./cmd/user-service", healthURL(user1HTTP, "/health"), combine(common, map[string]string{
		"USER_HTTP_ADDR":                  user1HTTP,
		"USER_GRPC_ADDR":                  user1GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR":     localGRPCAdvertise(user1GRPC),
		"SERVICE_INSTANCE_ID":             "user-service-1",
		"POSTGRES_MAX_OPEN_CONNS":         getenvOr("USER_POSTGRES_MAX_OPEN_CONNS", "64"),
		"POSTGRES_MAX_IDLE_CONNS":         getenvOr("USER_POSTGRES_MAX_IDLE_CONNS", "32"),
		"POSTGRES_CONN_MAX_LIFETIME_SEC":  getenvOr("POSTGRES_CONN_MAX_LIFETIME_SEC", "300"),
		"POSTGRES_CONN_MAX_IDLE_TIME_SEC": getenvOr("POSTGRES_CONN_MAX_IDLE_TIME_SEC", "120"),
	}))
	user2 := newGoRunService("user-service-2", "./cmd/user-service", healthURL(user2HTTP, "/health"), combine(common, map[string]string{
		"USER_HTTP_ADDR":                  user2HTTP,
		"USER_GRPC_ADDR":                  user2GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR":     localGRPCAdvertise(user2GRPC),
		"SERVICE_INSTANCE_ID":             "user-service-2",
		"POSTGRES_MAX_OPEN_CONNS":         getenvOr("USER_POSTGRES_MAX_OPEN_CONNS", "64"),
		"POSTGRES_MAX_IDLE_CONNS":         getenvOr("USER_POSTGRES_MAX_IDLE_CONNS", "32"),
		"POSTGRES_CONN_MAX_LIFETIME_SEC":  getenvOr("POSTGRES_CONN_MAX_LIFETIME_SEC", "300"),
		"POSTGRES_CONN_MAX_IDLE_TIME_SEC": getenvOr("POSTGRES_CONN_MAX_IDLE_TIME_SEC", "120"),
	}))

	friend1 := newGoRunService("friend-service-1", "./cmd/friend-service", healthURL(friend1HTTP, "/health"), combine(common, map[string]string{
		"FRIEND_HTTP_ADDR":            friend1HTTP,
		"FRIEND_GRPC_ADDR":            friend1GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(friend1GRPC),
		"SERVICE_INSTANCE_ID":         "friend-service-1",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("FRIEND_POSTGRES_MAX_OPEN_CONNS", "6"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("FRIEND_POSTGRES_MAX_IDLE_CONNS", "3"),
	}))
	auth1 := newGoRunService("auth-service-1", "./cmd/auth-service", healthURL(auth1HTTP, "/health"), combine(common, map[string]string{
		"AUTH_HTTP_ADDR":              auth1HTTP,
		"AUTH_GRPC_ADDR":              auth1GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(auth1GRPC),
		"SERVICE_INSTANCE_ID":         "auth-service-1",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("AUTH_POSTGRES_MAX_OPEN_CONNS", "32"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("AUTH_POSTGRES_MAX_IDLE_CONNS", "16"),
	}))
	auth2 := newGoRunService("auth-service-2", "./cmd/auth-service", healthURL(auth2HTTP, "/health"), combine(common, map[string]string{
		"AUTH_HTTP_ADDR":              auth2HTTP,
		"AUTH_GRPC_ADDR":              auth2GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(auth2GRPC),
		"SERVICE_INSTANCE_ID":         "auth-service-2",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("AUTH_POSTGRES_MAX_OPEN_CONNS", "32"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("AUTH_POSTGRES_MAX_IDLE_CONNS", "16"),
	}))

	conv1 := newGoRunService("conversation-service-1", "./cmd/conversation-service", healthURL(conv1HTTP, "/health"), combine(common, map[string]string{
		"CONVERSATION_HTTP_ADDR":      conv1HTTP,
		"CONVERSATION_GRPC_ADDR":      conv1GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(conv1GRPC),
		"SERVICE_INSTANCE_ID":         "conversation-service-1",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("CONVERSATION_POSTGRES_MAX_OPEN_CONNS", "8"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("CONVERSATION_POSTGRES_MAX_IDLE_CONNS", "4"),
	}))
	conv2 := newGoRunService("conversation-service-2", "./cmd/conversation-service", healthURL(conv2HTTP, "/health"), combine(common, map[string]string{
		"CONVERSATION_HTTP_ADDR":      conv2HTTP,
		"CONVERSATION_GRPC_ADDR":      conv2GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(conv2GRPC),
		"SERVICE_INSTANCE_ID":         "conversation-service-2",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("CONVERSATION_POSTGRES_MAX_OPEN_CONNS", "8"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("CONVERSATION_POSTGRES_MAX_IDLE_CONNS", "4"),
	}))
	conv3 := newGoRunService("conversation-service-3", "./cmd/conversation-service", healthURL(conv3HTTP, "/health"), combine(common, map[string]string{
		"CONVERSATION_HTTP_ADDR":      conv3HTTP,
		"CONVERSATION_GRPC_ADDR":      conv3GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(conv3GRPC),
		"SERVICE_INSTANCE_ID":         "conversation-service-3",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("CONVERSATION_POSTGRES_MAX_OPEN_CONNS", "8"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("CONVERSATION_POSTGRES_MAX_IDLE_CONNS", "4"),
	}))
	group1 := newGoRunService("group-service-1", "./cmd/group-service", healthURL(group1HTTP, "/health"), combine(common, map[string]string{
		"GROUP_HTTP_ADDR":             group1HTTP,
		"GROUP_GRPC_ADDR":             group1GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(group1GRPC),
		"SERVICE_INSTANCE_ID":         "group-service-1",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("GROUP_POSTGRES_MAX_OPEN_CONNS", "8"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("GROUP_POSTGRES_MAX_IDLE_CONNS", "4"),
	}))
	group2 := newGoRunService("group-service-2", "./cmd/group-service", healthURL(group2HTTP, "/health"), combine(common, map[string]string{
		"GROUP_HTTP_ADDR":             group2HTTP,
		"GROUP_GRPC_ADDR":             group2GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(group2GRPC),
		"SERVICE_INSTANCE_ID":         "group-service-2",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("GROUP_POSTGRES_MAX_OPEN_CONNS", "8"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("GROUP_POSTGRES_MAX_IDLE_CONNS", "4"),
	}))
	group3 := newGoRunService("group-service-3", "./cmd/group-service", healthURL(group3HTTP, "/health"), combine(common, map[string]string{
		"GROUP_HTTP_ADDR":             group3HTTP,
		"GROUP_GRPC_ADDR":             group3GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(group3GRPC),
		"SERVICE_INSTANCE_ID":         "group-service-3",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("GROUP_POSTGRES_MAX_OPEN_CONNS", "8"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("GROUP_POSTGRES_MAX_IDLE_CONNS", "4"),
	}))
	log1 := newGoRunService("log-service-1", "./cmd/log-service", healthURL(log1HTTP, "/health"), combine(common, map[string]string{
		"LOG_HTTP_ADDR":     log1HTTP,
		"ELASTICSEARCH_URL": getenvOr("ELASTICSEARCH_URL", "http://localhost:9200"),
	}))
	observe1 := newGoRunService("observe-service-1", "./cmd/observe-service", healthURL(observeHTTP, "/health"), combine(common, map[string]string{
		"OBSERVE_HTTP_ADDR":                  observeHTTP,
		"SERVICE_ADVERTISE_HTTP_ADDR":        getenvOr("OBSERVE1_SERVICE_ADVERTISE_HTTP_ADDR", "http://127.0.0.1"+observeHTTP),
		"SERVICE_INSTANCE_ID":                getenvOr("OBSERVE1_SERVICE_INSTANCE_ID", "observe-service-1"),
		"LOG_SERVICE_HTTP_URL":               getenvOr("LOG1_HTTP_URL", "http://localhost"+log1HTTP),
		"FILE_SERVICE_HTTP_URL":              getenvOr("FILE1_HTTP_URL", "http://localhost"+file1HTTP),
		"PROMETHEUS_QUERY_URL":               getenvOr("PROMETHEUS_QUERY_URL", "http://localhost:9090"),
		"OBSERVE_GATEWAY_METRICS_SCRAPE_URL": getenvOr("OBSERVE1_GATEWAY_METRICS_SCRAPE_URL", "http://127.0.0.1:26080/metrics"),
	}))

	file1 := newGoRunService("file-service-1", "./cmd/file-service", healthURL(file1HTTP, "/health"), combine(common, map[string]string{
		"FILE_HTTP_ADDR":              file1HTTP,
		"FILE_GRPC_ADDR":              file1GRPC,
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(file1GRPC),
		"SERVICE_INSTANCE_ID":         "file-service-1",
		"POSTGRES_MAX_OPEN_CONNS":     getenvOr("FILE_POSTGRES_MAX_OPEN_CONNS", "10"),
		"POSTGRES_MAX_IDLE_CONNS":     getenvOr("FILE_POSTGRES_MAX_IDLE_CONNS", "5"),
		"FILE_SERVICE_HTTP_URL":       "http://localhost" + file1HTTP,
	}))

	// gateway 未单独注册 /health，用 /metrics 判断 HTTP 已在听。
	gw1 := newGoRunService("gateway-1", "./cmd/gateway", healthURL(gw1HTTP, "/metrics"), combine(common, map[string]string{
		"GATEWAY_HTTP_ADDR":           gw1HTTP,
		"GATEWAY_PUSH_GRPC_ADDR":      getenvOr("GATEWAY1_PUSH_GRPC_ADDR", ":26090"),
		"GATEWAY_NODE_ID":             getenvOr("GATEWAY1_NODE_ID", "gateway-1"),
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(getenvOr("GATEWAY1_PUSH_GRPC_ADDR", ":26090")),
		"SERVICE_INSTANCE_ID":         "gateway-1",
		"LOG_SERVICE_HTTP_URL":        getenvOr("LOG1_HTTP_URL", "http://localhost"+log1HTTP),
		"FILE_SERVICE_HTTP_URL":       getenvOr("FILE1_HTTP_URL", "http://localhost"+file1HTTP),
	}))
	gw2 := newGoRunService("gateway-2", "./cmd/gateway", healthURL(gw2HTTP, "/metrics"), combine(common, map[string]string{
		"GATEWAY_HTTP_ADDR":           gw2HTTP,
		"GATEWAY_PUSH_GRPC_ADDR":      getenvOr("GATEWAY2_PUSH_GRPC_ADDR", ":26190"),
		"GATEWAY_NODE_ID":             getenvOr("GATEWAY2_NODE_ID", "gateway-2"),
		"SERVICE_ADVERTISE_GRPC_ADDR": localGRPCAdvertise(getenvOr("GATEWAY2_PUSH_GRPC_ADDR", ":26190")),
		"SERVICE_INSTANCE_ID":         "gateway-2",
		"LOG_SERVICE_HTTP_URL":        getenvOr("LOG1_HTTP_URL", "http://localhost"+log1HTTP),
		"FILE_SERVICE_HTTP_URL":       getenvOr("FILE1_HTTP_URL", "http://localhost"+file1HTTP),
	}))

	// 启动顺序：user → friend+auth → conv/group/log/observe → file → gateway
	waves := [][]*service{
		{user1, user2},
		{friend1, auth1, auth2},
		{conv1, conv2, conv3, group1, group2, group3, log1, observe1},
		{file1},
		{gw1, gw2},
	}

	var services []*service
	for wi, wave := range waves {
		if err := startWave(wave); err != nil {
			log.Fatal(err)
		}
		services = append(services, wave...)
		colorPrintf(ansiCyan, "[wave %d/%d] waiting ready...", wi+1, len(waves))
		if err := waitWaveReady(wave, 90*time.Second); err != nil {
			log.Fatal(err)
		}
	}

	colorPrintf(ansiGreen, "all services ready")
	printDemoLinks()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	colorPrintf(ansiYellow, "shutting down all services...")
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
