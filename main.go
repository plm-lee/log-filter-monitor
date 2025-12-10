package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"log-filter-monitor/internal/filter"
	"log-filter-monitor/internal/handler"
	"log-filter-monitor/internal/metrics"
	"log-filter-monitor/internal/monitor"
)

func main() {
	// 解析命令行参数
	logFile := flag.String("file", "", "要监控的日志文件路径（可选，如果规则中配置了log_file则不需要）")
	configFile := flag.String("config", "config.yaml", "配置文件路径（可选，默认：config.yaml）")
	flag.Parse()

	// 加载完整配置
	cfg, err := filter.LoadConfig(*configFile)
	if err != nil {
		log.Fatalf("加载配置文件失败: %v", err)
	}

	// 创建多文件监控管理器
	multiMonitor := monitor.NewMultiMonitor()

	// 确定需要监控的文件
	// 1. 如果规则中配置了 log_file，使用规则中的配置
	// 2. 如果规则中没有配置，使用全局文件（通过 -file 参数指定）
	monitoredFiles := make(map[string]bool) // 用于去重
	for _, rule := range cfg.Rules {
		filePath := rule.LogFile
		if filePath == "" {
			// 如果规则中没有配置 log_file，使用全局文件
			if *logFile == "" {
				log.Fatalf("错误：规则 '%s' 未配置 log_file，且未通过 -file 参数指定全局日志文件", rule.Name)
			}
			filePath = *logFile
		}
		if !monitoredFiles[filePath] {
			monitoredFiles[filePath] = true
			if err := multiMonitor.AddMonitor(filePath); err != nil {
				log.Fatalf("添加监控文件失败: %v", err)
			}
		}
	}

	// 如果没有监控任何文件，检查是否有全局文件
	if len(monitoredFiles) == 0 {
		if *logFile == "" {
			fmt.Println("错误：必须通过 -file 参数指定日志文件，或在规则中配置 log_file")
			fmt.Println("使用方法：")
			flag.PrintDefaults()
			os.Exit(1)
		}
		if err := multiMonitor.AddMonitor(*logFile); err != nil {
			log.Fatalf("启动日志监控失败: %v", err)
		}
	}

	// 创建日志过滤器
	logFilter, err := filter.NewLogFilter(cfg.Rules)
	if err != nil {
		log.Fatalf("创建日志过滤器失败: %v", err)
	}
	filterManager := filter.NewFilterManager(logFilter)

	// 创建日志处理器
	logHandler, err := handler.CreateHandler(cfg.Handler)
	if err != nil {
		log.Fatalf("创建日志处理器失败: %v", err)
	}

	// 创建指标管理器
	metricsManager, err := metrics.CreateMetricsManager(cfg.Metrics)
	if err != nil {
		log.Fatalf("创建指标管理器失败: %v", err)
	}

	// 创建通道
	stopChan := make(chan struct{})
	resultChan := make(chan filter.MatchResult, 100)

	// 启动指标统计
	if metricsManager != nil {
		metricsManager.Start(metrics.LogOutputFunc)
	}

	// 启动日志过滤（使用多监控管理器的输出通道）
	filterManager.Start(multiMonitor.GetOutputChan(), resultChan, stopChan)

	// 启动日志处理
	var metricsCollector handler.MetricsCollector
	globalMetricsEnabled := cfg.Metrics.Enabled
	if metricsManager != nil && metricsManager.GetCollector() != nil {
		metricsCollector = metricsManager.GetCollector()
	}
	handlerManager := handler.NewHandlerManager(logHandler, metricsCollector, globalMetricsEnabled)
	handlerManager.Start(resultChan, stopChan)

	log.Println("日志过滤监控系统已启动")

	// 设置信号处理，优雅退出
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("\n正在停止日志过滤监控系统...")

	// 关闭停止信号通道，通知所有goroutine退出
	close(stopChan)

	// 停止各个模块
	multiMonitor.Stop()
	filterManager.Wait()
	handlerManager.Wait()
	if metricsManager != nil {
		metricsManager.Stop()
	}

	// 输出最终统计信息
	if metricsManager != nil {
		finalMetrics := metricsManager.GetFinalMetrics()
		if finalMetrics.TotalCount > 0 {
			log.Println("\n========== 最终统计信息 ==========")
			log.Printf("总匹配数: %d\n", finalMetrics.TotalCount)
			if len(finalMetrics.RuleCounts) > 0 {
				log.Println("各规则匹配数:")
				for ruleName, count := range finalMetrics.RuleCounts {
					log.Printf("  - %s: %d\n", ruleName, count)
				}
			}
			log.Println("==================================")
		}
	}

	// 如果使用了HTTP处理器，输出统计信息
	if httpHandler, ok := logHandler.(*handler.HTTPHandler); ok {
		success, failed := httpHandler.GetStats()
		log.Printf("HTTP上报统计 - 成功: %d, 失败: %d\n", success, failed)
	}

	log.Println("日志过滤监控系统已停止")
}
