package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"aistudio-gemini-gateway/gateway"
)

// 构建时注入的版本信息。
var (
	version   = "dev"
	commit    = "unknown"
	buildTime = "unknown"
)

func main() {
	var (
		configPath string
		host       string
		port       int
		logLevel   string
		showVer    bool
	)

	flag.StringVar(&configPath, "config", "config.yaml", "配置文件路径")
	flag.StringVar(&host, "host", "", "覆盖 server.host")
	flag.IntVar(&port, "port", 0, "覆盖 server.port")
	flag.StringVar(&logLevel, "log-level", "", "覆盖 logging.level")
	flag.BoolVar(&showVer, "version", false, "显示版本信息并退出")
	flag.Parse()

	if showVer {
		fmt.Printf("AI Studio Gemini Gateway\n")
		fmt.Printf("  version:   %s\n", version)
		fmt.Printf("  commit:    %s\n", commit)
		fmt.Printf("  buildTime: %s\n", buildTime)
		return
	}

	// 加载配置。
	// optional=true 表示配置文件不存在时使用默认配置。
	cfg, err := gateway.LoadConfigBootstrap(configPath, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载配置失败: %v\n", err)
		os.Exit(1)
	}

	// 命令行参数优先级高于配置文件。
	if host != "" {
		cfg.Server.Host = host
	}
	if port > 0 {
		cfg.Server.Port = port
	}
	if logLevel != "" {
		cfg.Logging.Level = logLevel
	}

	// 创建服务。
	svc, err := gateway.NewService(gateway.Options{
		Config:    cfg,
		Version:   version,
		Commit:    commit,
		BuildTime: buildTime,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建服务失败: %v\n", err)
		os.Exit(1)
	}

	// 监听系统信号，用于优雅关闭。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 启动服务（阻塞，直到 ctx 取消或服务退出）。
	if err := svc.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "服务异常退出: %v\n", err)
		os.Exit(1)
	}
}
