package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"log-filter-monitor/internal/filter"
	"log-filter-monitor/internal/handler"
	"log-filter-monitor/internal/monitor"
)

func main() {
	// 解析命令行参数
	logFile := flag.String("file", "", "要监控的日志文件路径（必需）")
	configFile := flag.String("config", "config.yaml", "配置文件路径（可选，默认：config.yaml）")
	flag.Parse()

	if *logFile == "" {
		fmt.Println("错误：必须指定要监控的日志文件路径")
		fmt.Println("使用方法：")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// 加载完整配置（包括规则和处理器配置）
	cfg, err := filter.LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("加载配置文件失败: %v", err)
	}

	// 创建日志过滤器
	logFilter, err := filter.NewLogFilter(cfg.Rules)
	if err != nil {
		log.Fatalf("创建日志过滤器失败: %v", err)
	}

	// 解析超时时间（如果配置了）
	timeout := 10 * time.Second // 默认超时时间
	if cfg.Handler.Timeout != "" {
		parsedTimeout, err := time.ParseDuration(cfg.Handler.Timeout)
		if err != nil {
			log.Printf("警告：无法解析超时时间 '%s'，使用默认值 10s: %v\n", cfg.Handler.Timeout, err)
		} else {
			timeout = parsedTimeout
		}
	}

	// 创建日志处理器
	var logHandler handler.LogHandler
	switch cfg.Handler.Type {
	case "console":
		logHandler = handler.NewConsoleHandler()
		log.Println("使用控制台输出处理器")
	case "http":
		if cfg.Handler.APIURL == "" {
			log.Fatalf("错误：使用HTTP处理器时必须在配置文件中配置 api_url")
		}
		logHandler = handler.NewHTTPHandler(cfg.Handler.APIURL, timeout)
		log.Printf("使用HTTP上报处理器，API地址: %s，超时时间: %v\n", cfg.Handler.APIURL, timeout)
	default:
		log.Fatalf("错误：不支持的处理器类型 '%s'，支持的类型：console, http", cfg.Handler.Type)
	}

	// 创建日志监控器
	logMonitor := monitor.NewLogMonitor(*logFile)

	// 创建通道
	stopChan := make(chan struct{})
	resultChan := make(chan filter.MatchResult, 100) // 带缓冲的通道

	// 设置信号处理，优雅退出
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// 启动日志监控
	if err := logMonitor.Start(); err != nil {
		log.Fatalf("启动日志监控失败: %v", err)
	}

	// 启动日志过滤goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		logFilter.Filter(logMonitor.LogChan, resultChan, stopChan)
	}()

	// 启动日志处理goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		handler.Process(resultChan, stopChan, logHandler)
	}()

	log.Println("日志过滤监控系统已启动")

	// 等待退出信号
	<-sigChan
	log.Println("\n正在停止日志过滤监控系统...")

	// 关闭停止信号通道，通知所有goroutine退出
	close(stopChan)

	// 停止日志监控
	logMonitor.Stop()

	// 等待所有goroutine退出
	wg.Wait()

	// 如果使用了HTTP处理器，输出统计信息
	if httpHandler, ok := logHandler.(*handler.HTTPHandler); ok {
		success, failed := httpHandler.GetStats()
		log.Printf("HTTP上报统计 - 成功: %d, 失败: %d\n", success, failed)
	}

	log.Println("日志过滤监控系统已停止")
}
