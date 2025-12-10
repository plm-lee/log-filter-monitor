package metrics

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"log-filter-monitor/internal/filter"
)

// MetricsManager 指标管理器
// 负责管理指标收集器的创建和运行
type MetricsManager struct {
	collector *MetricsCollector
	wg        sync.WaitGroup
}

// MetricsCollector 指标收集器
// 负责统计匹配到的日志数量和指标
// 使用 sync.Map 和 atomic 操作优化高并发性能
type MetricsCollector struct {
	counters      sync.Map       // 每个规则的匹配计数（key: string, value: *int64）
	totalCounter  int64          // 总匹配计数（使用 atomic 操作）
	startTime     time.Time      // 开始时间
	lastResetTime time.Time      // 上次重置时间
	mu            sync.Mutex     // 保护时间字段的互斥锁
	interval      time.Duration  // 统计间隔（默认1分钟）
	stopChan      chan struct{}  // 停止信号通道
	wg            sync.WaitGroup // 等待组
}

// Metrics 指标数据
// 包含统计信息
type Metrics struct {
	Timestamp  int64            `json:"timestamp"`   // 时间戳
	RuleCounts map[string]int64 `json:"rule_counts"` // 每个规则的计数
	TotalCount int64            `json:"total_count"` // 总计数
	Duration   int64            `json:"duration"`    // 统计时长（秒）
}

// NewMetricsCollector 创建新的指标收集器
// interval: 统计间隔（默认1分钟）
// 返回: MetricsCollector实例
func NewMetricsCollector(interval time.Duration) *MetricsCollector {
	if interval <= 0 {
		interval = 1 * time.Minute
	}

	now := time.Now()
	return &MetricsCollector{
		counters:      sync.Map{},
		startTime:     now,
		lastResetTime: now,
		interval:      interval,
		stopChan:      make(chan struct{}),
	}
}

// Increment 增加指定规则的计数
// ruleName: 规则名称
// 使用 sync.Map 和 atomic 操作，无锁高性能
func (mc *MetricsCollector) Increment(ruleName string) {
	// 增加总计数
	atomic.AddInt64(&mc.totalCounter, 1)

	// 增加规则计数
	value, _ := mc.counters.LoadOrStore(ruleName, new(int64))
	counter := value.(*int64)
	atomic.AddInt64(counter, 1)
}

// IncrementByMatchResult 根据匹配结果增加计数
// matchResult: 匹配结果
func (mc *MetricsCollector) IncrementByMatchResult(matchResult filter.MatchResult) {
	mc.Increment(matchResult.Rule.Name)
}

// GetMetrics 获取当前指标快照
// 返回: 指标数据
func (mc *MetricsCollector) GetMetrics() Metrics {
	// 创建计数器的副本
	ruleCounts := make(map[string]int64)
	mc.counters.Range(func(key, value interface{}) bool {
		ruleName := key.(string)
		counter := value.(*int64)
		ruleCounts[ruleName] = atomic.LoadInt64(counter)
		return true
	})

	mc.mu.Lock()
	now := time.Now()
	duration := now.Sub(mc.lastResetTime).Seconds()
	mc.mu.Unlock()

	return Metrics{
		Timestamp:  now.Unix(),
		RuleCounts: ruleCounts,
		TotalCount: atomic.LoadInt64(&mc.totalCounter),
		Duration:   int64(duration),
	}
}

// Reset 重置统计计数器
func (mc *MetricsCollector) Reset() {
	// 重置所有计数器
	mc.counters.Range(func(key, value interface{}) bool {
		counter := value.(*int64)
		atomic.StoreInt64(counter, 0)
		return true
	})

	atomic.StoreInt64(&mc.totalCounter, 0)

	mc.mu.Lock()
	mc.lastResetTime = time.Now()
	mc.mu.Unlock()
}

// GetAndReset 获取当前指标并重置计数器
// 返回: 重置前的指标数据
func (mc *MetricsCollector) GetAndReset() Metrics {
	// 创建计数器的副本并重置
	ruleCounts := make(map[string]int64)
	mc.counters.Range(func(key, value interface{}) bool {
		ruleName := key.(string)
		counter := value.(*int64)
		ruleCounts[ruleName] = atomic.SwapInt64(counter, 0) // 原子交换并返回旧值
		return true
	})

	mc.mu.Lock()
	now := time.Now()
	duration := now.Sub(mc.lastResetTime).Seconds()
	mc.lastResetTime = now
	mc.mu.Unlock()

	totalCount := atomic.SwapInt64(&mc.totalCounter, 0)

	return Metrics{
		Timestamp:  now.Unix(),
		RuleCounts: ruleCounts,
		TotalCount: totalCount,
		Duration:   int64(duration),
	}
}

// Start 启动定期统计输出
// outputFunc: 输出函数，用于输出统计信息
func (mc *MetricsCollector) Start(outputFunc func(Metrics)) {
	mc.wg.Add(1)
	go mc.periodicReport(outputFunc)
}

// periodicReport 定期报告统计信息
// outputFunc: 输出函数
func (mc *MetricsCollector) periodicReport(outputFunc func(Metrics)) {
	defer mc.wg.Done()

	ticker := time.NewTicker(mc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-mc.stopChan:
			return
		case <-ticker.C:
			metrics := mc.GetAndReset()
			outputFunc(metrics)
		}
	}
}

// Stop 停止指标收集器
func (mc *MetricsCollector) Stop() {
	close(mc.stopChan)
	mc.wg.Wait()
}

// GetTotalCount 获取总计数
// 返回: 总计数
func (mc *MetricsCollector) GetTotalCount() int64 {
	return atomic.LoadInt64(&mc.totalCounter)
}

// GetRuleCount 获取指定规则的计数
// ruleName: 规则名称
// 返回: 该规则的计数
func (mc *MetricsCollector) GetRuleCount(ruleName string) int64 {
	value, ok := mc.counters.Load(ruleName)
	if !ok {
		return 0
	}
	counter := value.(*int64)
	return atomic.LoadInt64(counter)
}

// FormatMetrics 格式化指标为字符串
// metrics: 指标数据
// 返回: 格式化后的字符串
func FormatMetrics(metrics Metrics) string {
	// 使用 strings.Builder 优化字符串拼接性能
	var builder strings.Builder
	builder.Grow(512) // 预分配容量，减少内存重新分配

	timestampStr := time.Unix(metrics.Timestamp, 0).Format("2006-01-02 15:04:05")
	builder.WriteString("\n========== 指标统计 [")
	builder.WriteString(timestampStr)
	builder.WriteString("] ==========\n")
	builder.WriteString(fmt.Sprintf("统计时长: %d 秒\n", metrics.Duration))
	builder.WriteString(fmt.Sprintf("总匹配数: %d\n", metrics.TotalCount))

	if len(metrics.RuleCounts) > 0 {
		builder.WriteString("各规则匹配数:\n")
		for ruleName, count := range metrics.RuleCounts {
			builder.WriteString(fmt.Sprintf("  - %s: %d\n", ruleName, count))
		}
	} else {
		builder.WriteString("各规则匹配数: 0\n")
	}

	builder.WriteString("==========================================\n")
	return builder.String()
}

// DefaultOutputFunc 默认输出函数
// 输出到标准输出
func DefaultOutputFunc(metrics Metrics) {
	fmt.Print(FormatMetrics(metrics))
}

// LogOutputFunc 日志输出函数
// 输出到日志
func LogOutputFunc(metrics Metrics) {
	log.Print(FormatMetrics(metrics))
}

// CreateMetricsManager 根据配置创建指标管理器
// metricsConfig: 指标配置
// 返回: MetricsManager实例和错误信息
func CreateMetricsManager(metricsConfig filter.MetricsConfig) (*MetricsManager, error) {
	if !metricsConfig.Enabled {
		log.Println("指标统计已禁用")
		return nil, nil
	}

	var interval time.Duration = 1 * time.Minute

	// 解析统计间隔
	if metricsConfig.Interval != "" {
		parsedInterval, err := time.ParseDuration(metricsConfig.Interval)
		if err != nil {
			log.Printf("警告：无法解析指标统计间隔 '%s'，使用默认值 1m: %v\n", metricsConfig.Interval, err)
		} else {
			interval = parsedInterval
		}
	}

	collector := NewMetricsCollector(interval)
	manager := &MetricsManager{
		collector: collector,
	}

	log.Printf("指标统计已启动（每 %v 输出一次）\n", interval)
	return manager, nil
}

// Start 启动指标统计
// outputFunc: 输出函数
func (mm *MetricsManager) Start(outputFunc func(Metrics)) {
	if mm.collector != nil {
		mm.collector.Start(outputFunc)
	}
}

// Stop 停止指标统计
func (mm *MetricsManager) Stop() {
	if mm.collector != nil {
		mm.collector.Stop()
	}
}

// GetCollector 获取指标收集器
// 返回: 指标收集器（可能为 nil）
func (mm *MetricsManager) GetCollector() *MetricsCollector {
	return mm.collector
}

// GetFinalMetrics 获取最终统计信息
// 返回: 指标数据
func (mm *MetricsManager) GetFinalMetrics() Metrics {
	if mm.collector != nil {
		return mm.collector.GetMetrics()
	}
	return Metrics{}
}
