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
	services := []*service{
		newGoRunService("auth-service", "./cmd/auth-service"),
		newGoRunService("user-service", "./cmd/user-service"),
		newGoRunService("friend-service", "./cmd/friend-service"),
		newGoRunService("conversation-service", "./cmd/conversation-service"),
		newGoRunService("gateway", "./cmd/gateway"),
	}

	// 启动所有子服务
	for _, s := range services {
		log.Printf("starting %s ...", s.Name)
		if err := s.Cmd.Start(); err != nil {
			log.Fatalf("failed to start %s: %v", s.Name, err)
		}
	}

	log.Println("all services started")

	// 监听 Ctrl+C，退出时尽量杀掉子进程
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
