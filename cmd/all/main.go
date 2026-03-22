package main

import (
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
)

type service struct {
	Name string
	Cmd  *exec.Cmd
}

func newGoRunService(name, path string) *service {
	// path 是子服务的 cmd 目录，比如 ./cmd/auth-service
	cmd := exec.Command("go", "run", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return &service{
		Name: name,
		Cmd:  cmd,
	}
}

func main() {
	// 1) 统一拉起核心服务，便于本地联调（等价于多开终端 go run）。
	services := []*service{
		newGoRunService("auth-service", "./cmd/auth-service"),
		newGoRunService("user-service", "./cmd/user-service"),
		newGoRunService("friend-service", "./cmd/friend-service"),
		newGoRunService("conversation-service", "./cmd/conversation-service"),
		newGoRunService("gateway", "./cmd/gateway"),
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
