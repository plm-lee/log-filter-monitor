package monitor

import (
	"fmt"
	"log"
	"regexp"
	"sync"
	"time"

	"log-filter-monitor/internal/filter"

	"github.com/hpcloud/tail"
)

// ruleMatcher 规则匹配器结构体
// 将规则和编译后的正则表达式匹配器关联起来
type ruleMatcher struct {
	rule    filter.Rule    // 过滤规则
	matcher *regexp.Regexp // 编译后的正则表达式匹配器
}

// LogMonitor 日志监控器结构体
// 负责实时监控日志文件并根据规则过滤日志
type LogMonitor struct {
	filePath string         // 要监控的日志文件路径
	rules    []filter.Rule  // 过滤规则列表（保留用于日志输出）
	tail     *tail.Tail     // tail实例
	stopChan chan struct{}  // 停止信号通道
	wg       sync.WaitGroup // 等待组，用于优雅关闭
	matchers []ruleMatcher  // 编译后的规则匹配器列表
}

// NewLogMonitor 创建新的日志监控器实例
// filePath: 要监控的日志文件路径
// rules: 过滤规则列表
// 返回: LogMonitor实例
func NewLogMonitor(filePath string, rules []filter.Rule) *LogMonitor {
	// 编译所有规则的正则表达式，只保留编译成功的规则
	matchers := make([]ruleMatcher, 0, len(rules))
	validRules := make([]filter.Rule, 0, len(rules))

	for _, rule := range rules {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			log.Printf("警告：规则 '%s' 的正则表达式编译失败: %v，将跳过此规则", rule.Name, err)
			continue
		}
		matchers = append(matchers, ruleMatcher{
			rule:    rule,
			matcher: re,
		})
		validRules = append(validRules, rule)
	}

	// 检查是否有有效的规则
	if len(matchers) == 0 {
		log.Printf("警告：没有有效的过滤规则，所有规则的正则表达式编译都失败了")
	}

	return &LogMonitor{
		filePath: filePath,
		rules:    validRules, // 只保留有效的规则
		stopChan: make(chan struct{}),
		matchers: matchers,
	}
}

// Start 启动日志监控
// 开始实时监控日志文件并输出匹配的日志行
// 返回: 错误信息（如果有）
func (lm *LogMonitor) Start() error {
	// 配置tail选项
	config := tail.Config{
		Follow:    true,                                 // 持续跟踪文件变化
		ReOpen:    true,                                 // 文件被移动或删除后重新打开（支持日志轮转）
		MustExist: false,                                // 文件不存在时不报错，等待文件创建
		Poll:      true,                                 // 使用轮询方式检测文件变化（兼容性更好）
		Location:  &tail.SeekInfo{Offset: 0, Whence: 2}, // 从文件末尾开始读取
	}

	// 打开文件进行监控
	t, err := tail.TailFile(lm.filePath, config)
	if err != nil {
		return fmt.Errorf("无法打开日志文件 %s: %w", lm.filePath, err)
	}

	lm.tail = t

	// 启动goroutine处理日志行
	lm.wg.Add(1)
	go lm.processLines()

	log.Printf("开始监控日志文件: %s\n", lm.filePath)
	log.Printf("已加载 %d 条过滤规则\n", len(lm.rules))

	return nil
}

// processLines 处理日志行的goroutine
// 从tail.Lines通道读取日志行，并根据规则进行过滤和输出
func (lm *LogMonitor) processLines() {
	defer lm.wg.Done()

	for {
		select {
		case <-lm.stopChan:
			return
		case line, ok := <-lm.tail.Lines:
			if !ok {
				// 通道已关闭
				return
			}
			if line.Err != nil {
				log.Printf("读取日志行时出错: %v\n", line.Err)
				continue
			}

			// 检查日志行是否匹配任何规则
			lm.checkAndOutput(line.Text)
		}
	}
}

// checkAndOutput 检查日志行是否匹配规则，如果匹配则输出
// line: 要检查的日志行内容
func (lm *LogMonitor) checkAndOutput(line string) {
	// 遍历所有匹配器
	for _, rm := range lm.matchers {
		if rm.matcher.MatchString(line) {
			// 输出匹配的日志，包含规则名称和时间戳
			timestamp := time.Now().Format("2006-01-02 15:04:05")
			fmt.Printf("[%s] [规则: %s] %s\n", timestamp, rm.rule.Name, line)

			// 如果规则有描述，也输出描述信息
			if rm.rule.Description != "" {
				fmt.Printf("  -> %s\n", rm.rule.Description)
			}
		}
	}
}

// Stop 停止日志监控
// 优雅地关闭tail实例和所有goroutine
func (lm *LogMonitor) Stop() {
	close(lm.stopChan)
	if lm.tail != nil {
		lm.tail.Stop()
	}
	lm.wg.Wait()
}
