package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"log-filter-monitor/internal/filter"
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

	// 加载过滤规则配置
	rules, err := filter.LoadRules(*configFile)
	if err != nil {
		log.Printf("警告：无法加载配置文件 %s，将使用默认规则: %v\n", *configFile, err)
		// 使用默认规则
		rules = []filter.Rule{
			{
				Name:        "错误日志",
				Pattern:     "ERROR|FATAL|CRITICAL",
				Description: "匹配包含ERROR、FATAL或CRITICAL的日志",
			},
		}
	}

	// 创建日志监控器
	logMonitor := monitor.NewLogMonitor(*logFile, rules)

	// 设置信号处理，优雅退出
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// 启动监控
	go func() {
		if err := logMonitor.Start(); err != nil {
			log.Fatalf("启动日志监控失败: %v", err)
		}
	}()

	// 等待退出信号
	<-sigChan
	log.Println("\n正在停止日志监控...")
	logMonitor.Stop()
	log.Println("日志监控已停止")
}
