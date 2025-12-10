package app

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

// App 应用管理器
// 负责管理整个应用的初始化、启动和关闭
type App struct {
	cfg            *filter.Config
	multiMonitor   *monitor.MultiMonitor
	filterManager  *filter.FilterManager
	handlerManager *handler.HandlerManager
	metricsManager *metrics.MetricsManager
	
	stopChan   chan struct{}
	resultChan chan filter.MatchResult
}

// NewApp 创建应用实例
// 返回: App实例
func NewApp() *App {
	return &App{
		stopChan:   make(chan struct{}),
		resultChan: make(chan filter.MatchResult, 100),
	}
}

// InitAll 初始化所有模块
// configFile: 配置文件路径
// logFile: 全局日志文件路径（可选）
// 返回: 错误信息（如果有）
func (a *App) InitAll(configFile string, logFile string) error {
	// 加载配置
	cfg, err := filter.LoadConfig(configFile)
	if err != nil {
		return fmt.Errorf("加载配置文件失败: %w", err)
	}
	a.cfg = cfg

	// 初始化监控模块
	if err := a.initMonitor(logFile); err != nil {
		return fmt.Errorf("初始化监控模块失败: %w", err)
	}

	// 初始化过滤模块
	if err := a.initFilter(); err != nil {
		return fmt.Errorf("初始化过滤模块失败: %w", err)
	}

	// 初始化指标模块（需要在 handler 之前初始化，因为 handler 依赖 metrics）
	if err := a.initMetrics(); err != nil {
		return fmt.Errorf("初始化指标模块失败: %w", err)
	}

	// 初始化处理模块
	if err := a.initHandler(); err != nil {
		return fmt.Errorf("初始化处理模块失败: %w", err)
	}

	log.Println("所有模块初始化完成")
	return nil
}

// initMonitor 初始化监控模块
func (a *App) initMonitor(globalLogFile string) error {
	a.multiMonitor = monitor.NewMultiMonitor()

	// 确定需要监控的文件
	monitoredFiles := make(map[string]bool) // 用于去重
	
	for _, rule := range a.cfg.Rules {
		filePath := rule.LogFile
		if filePath == "" {
			// 如果规则中没有配置 log_file，使用全局文件
			if globalLogFile == "" {
				return fmt.Errorf("规则 '%s' 未配置 log_file，且未通过 -file 参数指定全局日志文件", rule.Name)
			}
			filePath = globalLogFile
		}
		
		if !monitoredFiles[filePath] {
			monitoredFiles[filePath] = true
			if err := a.multiMonitor.AddMonitor(filePath); err != nil {
				return fmt.Errorf("添加监控文件失败: %w", err)
			}
		}
	}

	// 如果没有监控任何文件，检查是否有全局文件
	if len(monitoredFiles) == 0 {
		if globalLogFile == "" {
			return fmt.Errorf("必须通过 -file 参数指定日志文件，或在规则中配置 log_file")
		}
		if err := a.multiMonitor.AddMonitor(globalLogFile); err != nil {
			return fmt.Errorf("启动日志监控失败: %w", err)
		}
	}

	return nil
}

// initFilter 初始化过滤模块
func (a *App) initFilter() error {
	logFilter, err := filter.NewLogFilter(a.cfg.Rules)
	if err != nil {
		return err
	}
	a.filterManager = filter.NewFilterManager(logFilter)
	return nil
}

// initHandler 初始化处理模块
func (a *App) initHandler() error {
	logHandler, err := handler.CreateHandler(a.cfg.Handler)
	if err != nil {
		return err
	}

	var metricsCollector handler.MetricsCollector
	globalMetricsEnabled := a.cfg.Metrics.Enabled
	if a.metricsManager != nil && a.metricsManager.GetCollector() != nil {
		metricsCollector = a.metricsManager.GetCollector()
	}

	a.handlerManager = handler.NewHandlerManager(logHandler, metricsCollector, globalMetricsEnabled)
	return nil
}

// initMetrics 初始化指标模块
func (a *App) initMetrics() error {
	metricsManager, err := metrics.CreateMetricsManager(a.cfg.Metrics)
	if err != nil {
		return err
	}
	a.metricsManager = metricsManager
	return nil
}

// Start 启动所有服务
func (a *App) Start() {
	// 启动指标统计
	if a.metricsManager != nil {
		a.metricsManager.Start(metrics.LogOutputFunc)
	}

	// 启动日志过滤
	a.filterManager.Start(a.multiMonitor.GetOutputChan(), a.resultChan, a.stopChan)

	// 启动日志处理
	a.handlerManager.Start(a.resultChan, a.stopChan)

	log.Println("日志过滤监控系统已启动")
}

// Wait 等待退出信号
func (a *App) Wait() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan
}

// Stop 停止所有服务
func (a *App) Stop() {
	log.Println("\n正在停止日志过滤监控系统...")

	// 关闭停止信号通道，通知所有goroutine退出
	close(a.stopChan)

	// 停止各个模块
	a.multiMonitor.Stop()
	a.filterManager.Wait()
	a.handlerManager.Wait()
	if a.metricsManager != nil {
		a.metricsManager.Stop()
	}

	// 输出最终统计信息
	a.printFinalStats()

	log.Println("日志过滤监控系统已停止")
}

// printFinalStats 输出最终统计信息
func (a *App) printFinalStats() {
	// 输出指标统计信息
	if a.metricsManager != nil {
		finalMetrics := a.metricsManager.GetFinalMetrics()
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

	// 输出HTTP处理器统计信息
	if a.handlerManager != nil {
		logHandler := a.handlerManager.GetHandler()
		if httpHandler, ok := logHandler.(*handler.HTTPHandler); ok {
			success, failed := httpHandler.GetStats()
			log.Printf("HTTP上报统计 - 成功: %d, 失败: %d\n", success, failed)
		}
	}
}

// ParseFlags 解析命令行参数
// 返回: configFile 和 logFile
func ParseFlags() (configFile string, logFile string) {
	logFileFlag := flag.String("file", "", "要监控的日志文件路径（可选，如果规则中配置了log_file则不需要）")
	configFileFlag := flag.String("config", "config.yaml", "配置文件路径（可选，默认：config.yaml）")
	flag.Parse()

	return *configFileFlag, *logFileFlag
}

// ValidateFlags 验证命令行参数
// logFile: 全局日志文件路径
// 返回: 错误信息（如果有）
func ValidateFlags(logFile string) error {
	// 如果既没有全局文件，也没有规则配置的文件，会在 InitAll 时检查
	// 这里只做基本的参数验证
	if logFile == "" {
		// 允许为空，因为可以在规则中配置
	}
	return nil
}

