package metrics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"log-filter-monitor/internal/filter"
)

// HTTPClient HTTP客户端接口
// 用于抽象HTTP请求，方便测试和扩展
type HTTPClient interface {
	Post(url string, data interface{}) error
}

// defaultHTTPClient 默认HTTP客户端实现
type defaultHTTPClient struct {
	client  *http.Client
	timeout time.Duration
}

// NewDefaultHTTPClient 创建默认HTTP客户端
func NewDefaultHTTPClient(timeout time.Duration) *defaultHTTPClient {
	return &defaultHTTPClient{
		client: &http.Client{
			Timeout: timeout,
		},
		timeout: timeout,
	}
}

// Post 发送POST请求
func (c *defaultHTTPClient) Post(url string, data interface{}) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("序列化数据失败: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("创建HTTP请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("发送HTTP请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP请求失败，状态码: %d", resp.StatusCode)
	}

	return nil
}

// MetricsManager 指标管理器
// 负责管理指标收集器的创建和运行
type MetricsManager struct {
	collector  *MetricsCollector
	wg         sync.WaitGroup
	httpClient HTTPClient // HTTP客户端（可选，用于上报指标）
	apiURL     string     // 指标上报API地址（可选）
}

// RuleMetrics 规则指标统计（按标签维度）
// 参考 falcon-log-agent 的设计，支持按标签分组统计
type RuleMetrics struct {
	sync.RWMutex
	// TagCounts: 按标签字符串（SortedTags）索引的计数
	// 例如：tagstring="env=prod,service=api" -> count
	TagCounts map[string]int64 // tagstring -> count
}

// MetricsCollector 指标收集器
// 负责统计匹配到的日志数量和指标
// 使用 sync.Map 和 atomic 操作优化高并发性能
// 参考 falcon-log-agent 的设计，支持按标签维度统计
type MetricsCollector struct {
	// ruleMetrics: 每个规则的指标统计（key: ruleName, value: *RuleMetrics）
	ruleMetrics   sync.Map
	totalCounter  int64          // 总匹配计数（使用 atomic 操作）
	startTime     time.Time      // 开始时间
	lastResetTime time.Time      // 上次重置时间
	mu            sync.Mutex     // 保护时间字段的互斥锁
	interval      time.Duration  // 统计间隔（默认1分钟）
	intervalSec   int64          // 统计间隔（秒），用于时间对齐
	stopChan      chan struct{}  // 停止信号通道
	wg            sync.WaitGroup // 等待组
}

// RuleMetricsData 规则指标数据（按标签维度）
type RuleMetricsData struct {
	RuleName   string           `json:"rule_name"`   // 规则名称
	TagCounts  map[string]int64 `json:"tag_counts"`  // 按标签维度的计数
	TotalCount int64            `json:"total_count"` // 该规则的总计数
}

// Metrics 指标数据
// 包含统计信息
type Metrics struct {
	Timestamp   int64                       `json:"timestamp"`    // 时间戳（对齐后的）
	RuleMetrics map[string]*RuleMetricsData `json:"rule_metrics"` // 每个规则的指标（按标签维度）
	TotalCount  int64                       `json:"total_count"`  // 总计数
	Duration    int64                       `json:"duration"`     // 统计时长（秒）

	// 向后兼容字段
	RuleCounts map[string]int64 `json:"rule_counts,omitempty"` // 每个规则的总计数（兼容旧格式）
}

// NewMetricsCollector 创建新的指标收集器
// interval: 统计间隔（默认1分钟）
// 返回: MetricsCollector实例
func NewMetricsCollector(interval time.Duration) *MetricsCollector {
	if interval <= 0 {
		interval = 1 * time.Minute
	}

	now := time.Now()
	intervalSec := int64(interval.Seconds())
	if intervalSec <= 0 {
		intervalSec = 60 // 默认60秒
	}

	return &MetricsCollector{
		ruleMetrics:   sync.Map{},
		startTime:     now,
		lastResetTime: now,
		interval:      interval,
		intervalSec:   intervalSec,
		stopChan:      make(chan struct{}),
	}
}

// getOrCreateRuleMetrics 获取或创建规则指标统计
func (mc *MetricsCollector) getOrCreateRuleMetrics(ruleName string) *RuleMetrics {
	value, _ := mc.ruleMetrics.LoadOrStore(ruleName, &RuleMetrics{
		TagCounts: make(map[string]int64),
	})
	return value.(*RuleMetrics)
}

// Increment 增加指定规则的计数
// ruleName: 规则名称
// tags: 标签 map（可选，用于按标签维度统计）
func (mc *MetricsCollector) Increment(ruleName string, tags map[string]string) {
	// 增加总计数
	atomic.AddInt64(&mc.totalCounter, 1)

	// 获取或创建规则指标
	ruleMetrics := mc.getOrCreateRuleMetrics(ruleName)

	// 构建标签字符串索引
	tagString := SortedTags(tags)
	if tagString == "" {
		tagString = "default" // 如果没有标签，使用默认值
	}

	// 增加该标签维度的计数
	ruleMetrics.Lock()
	ruleMetrics.TagCounts[tagString]++
	ruleMetrics.Unlock()
}

// IncrementByMatchResult 根据匹配结果增加计数
// matchResult: 匹配结果
// 自动提取规则标签，支持按标签维度统计
func (mc *MetricsCollector) IncrementByMatchResult(matchResult filter.MatchResult) {
	// 构建标签 map
	tags := make(map[string]string)
	if matchResult.Tag != "" {
		tags["tag"] = matchResult.Tag
	}
	if matchResult.LogFile != "" {
		tags["log_file"] = matchResult.LogFile
	}

	mc.Increment(matchResult.Rule.Name, tags)
}

// GetMetrics 获取当前指标快照
// 返回: 指标数据
func (mc *MetricsCollector) GetMetrics() Metrics {
	mc.mu.Lock()
	now := time.Now()
	currentTms := now.Unix()
	// 对齐时间戳到最近的 interval
	alignedTms := AlignStepTms(mc.intervalSec, currentTms)
	duration := now.Sub(mc.lastResetTime).Seconds()
	mc.mu.Unlock()

	// 收集所有规则的指标数据（按标签维度）
	ruleMetricsData := make(map[string]*RuleMetricsData)
	ruleCounts := make(map[string]int64) // 向后兼容

	mc.ruleMetrics.Range(func(key, value interface{}) bool {
		ruleName := key.(string)
		ruleMetrics := value.(*RuleMetrics)

		ruleMetrics.RLock()
		// 创建标签计数的副本
		tagCounts := make(map[string]int64, len(ruleMetrics.TagCounts))
		var totalCount int64
		for tagString, count := range ruleMetrics.TagCounts {
			tagCounts[tagString] = count
			totalCount += count
		}
		ruleMetrics.RUnlock()

		ruleMetricsData[ruleName] = &RuleMetricsData{
			RuleName:   ruleName,
			TagCounts:  tagCounts,
			TotalCount: totalCount,
		}
		ruleCounts[ruleName] = totalCount // 向后兼容
		return true
	})

	return Metrics{
		Timestamp:   alignedTms,
		RuleMetrics: ruleMetricsData,
		TotalCount:  atomic.LoadInt64(&mc.totalCounter),
		Duration:    int64(duration),
		RuleCounts:  ruleCounts, // 向后兼容
	}
}

// Reset 重置统计计数器
func (mc *MetricsCollector) Reset() {
	// 重置所有规则指标
	mc.ruleMetrics.Range(func(key, value interface{}) bool {
		ruleMetrics := value.(*RuleMetrics)
		ruleMetrics.Lock()
		ruleMetrics.TagCounts = make(map[string]int64)
		ruleMetrics.Unlock()
		return true
	})

	atomic.StoreInt64(&mc.totalCounter, 0)

	mc.mu.Lock()
	mc.lastResetTime = time.Now()
	mc.mu.Unlock()
}

// GetAndReset 获取当前指标并重置计数器
// 返回: 重置前的指标数据（时间戳已对齐）
func (mc *MetricsCollector) GetAndReset() Metrics {
	mc.mu.Lock()
	now := time.Now()
	currentTms := now.Unix()
	// 对齐时间戳到最近的 interval
	alignedTms := AlignStepTms(mc.intervalSec, currentTms)
	duration := now.Sub(mc.lastResetTime).Seconds()
	mc.lastResetTime = now
	mc.mu.Unlock()

	// 收集所有规则的指标数据并重置
	ruleMetricsData := make(map[string]*RuleMetricsData)
	ruleCounts := make(map[string]int64) // 向后兼容

	mc.ruleMetrics.Range(func(key, value interface{}) bool {
		ruleName := key.(string)
		ruleMetrics := value.(*RuleMetrics)

		ruleMetrics.Lock()
		// 创建标签计数的副本
		tagCounts := make(map[string]int64, len(ruleMetrics.TagCounts))
		var totalCount int64
		for tagString, count := range ruleMetrics.TagCounts {
			tagCounts[tagString] = count
			totalCount += count
		}
		// 重置
		ruleMetrics.TagCounts = make(map[string]int64)
		ruleMetrics.Unlock()

		ruleMetricsData[ruleName] = &RuleMetricsData{
			RuleName:   ruleName,
			TagCounts:  tagCounts,
			TotalCount: totalCount,
		}
		ruleCounts[ruleName] = totalCount // 向后兼容
		return true
	})

	totalCount := atomic.SwapInt64(&mc.totalCounter, 0)

	return Metrics{
		Timestamp:   alignedTms,
		RuleMetrics: ruleMetricsData,
		TotalCount:  totalCount,
		Duration:    int64(duration),
		RuleCounts:  ruleCounts, // 向后兼容
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

// GetRuleCount 获取指定规则的总计数
// ruleName: 规则名称
// 返回: 该规则的总计数
func (mc *MetricsCollector) GetRuleCount(ruleName string) int64 {
	value, ok := mc.ruleMetrics.Load(ruleName)
	if !ok {
		return 0
	}
	ruleMetrics := value.(*RuleMetrics)

	ruleMetrics.RLock()
	var total int64
	for _, count := range ruleMetrics.TagCounts {
		total += count
	}
	ruleMetrics.RUnlock()

	return total
}

// FormatMetrics 格式化指标为字符串
// metrics: 指标数据
// 返回: 格式化后的字符串
func FormatMetrics(metrics Metrics) string {
	// 使用 strings.Builder 优化字符串拼接性能
	var builder strings.Builder
	builder.Grow(1024) // 预分配更大的容量，支持标签维度显示

	timestampStr := time.Unix(metrics.Timestamp, 0).Format("2006-01-02 15:04:05")
	builder.WriteString("\n========== 指标统计 [")
	builder.WriteString(timestampStr)
	builder.WriteString("] ==========\n")
	builder.WriteString(fmt.Sprintf("统计时长: %d 秒\n", metrics.Duration))
	builder.WriteString(fmt.Sprintf("总匹配数: %d\n", metrics.TotalCount))

	if len(metrics.RuleMetrics) > 0 {
		builder.WriteString("各规则匹配数（按标签维度）:\n")
		for ruleName, ruleData := range metrics.RuleMetrics {
			builder.WriteString(fmt.Sprintf("  - %s (总计: %d):\n", ruleName, ruleData.TotalCount))
			if len(ruleData.TagCounts) > 0 {
				for tagString, count := range ruleData.TagCounts {
					if tagString == "default" {
						builder.WriteString(fmt.Sprintf("    * 无标签: %d\n", count))
					} else {
						builder.WriteString(fmt.Sprintf("    * [%s]: %d\n", tagString, count))
					}
				}
			}
		}
	} else if len(metrics.RuleCounts) > 0 {
		// 向后兼容：如果没有 RuleMetrics，使用 RuleCounts
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

	// 如果配置了指标上报API地址，初始化HTTP客户端
	if metricsConfig.APIURL != "" {
		var timeout time.Duration = 10 * time.Second
		if metricsConfig.Timeout != "" {
			parsedTimeout, err := time.ParseDuration(metricsConfig.Timeout)
			if err != nil {
				log.Printf("警告：无法解析指标上报超时时间 '%s'，使用默认值 10s: %v\n", metricsConfig.Timeout, err)
			} else {
				timeout = parsedTimeout
			}
		}

		manager.apiURL = metricsConfig.APIURL
		manager.httpClient = NewDefaultHTTPClient(timeout)
		log.Printf("指标统计已启动（每 %v 输出一次，并上报到: %s）\n", interval, metricsConfig.APIURL)
	} else {
		log.Printf("指标统计已启动（每 %v 输出一次）\n", interval)
	}

	return manager, nil
}

// Start 启动指标统计
// outputFunc: 输出函数（用于输出到控制台或日志）
func (mm *MetricsManager) Start(outputFunc func(Metrics)) {
	if mm.collector != nil {
		// 创建组合输出函数：同时输出到控制台/日志和HTTP接口（如果配置了）
		combinedOutputFunc := func(metrics Metrics) {
			// 指标统计为 0 时不输出日志、不上报
			if metrics.TotalCount == 0 {
				return
			}

			// 输出到控制台/日志
			if outputFunc != nil {
				outputFunc(metrics)
			}

			// 如果配置了HTTP接口，上报指标数据
			if mm.httpClient != nil && mm.apiURL != "" {
				if err := mm.reportMetrics(metrics); err != nil {
					log.Printf("指标上报失败: %v\n", err)
				}
			}
		}

		mm.collector.Start(combinedOutputFunc)
	}
}

// reportMetrics 上报指标数据到HTTP接口
// metrics: 指标数据
// 返回: 错误信息（如果有）
// 参考 falcon-log-agent 的设计，将指标数据转换为点格式数组
func (mm *MetricsManager) reportMetrics(metrics Metrics) error {
	// 构建上报数据点数组（参考 falcon-log-agent 的 FalconPoint 格式）
	points := make([]map[string]interface{}, 0)

	// 为每个规则的每个标签维度创建一个数据点
	for ruleName, ruleData := range metrics.RuleMetrics {
		for tagString, count := range ruleData.TagCounts {
			point := map[string]interface{}{
				"metric":      ruleName,                 // 指标名称（规则名称）
				"timestamp":   metrics.Timestamp,        // 时间戳（已对齐）
				"step":        mm.collector.intervalSec, // 统计间隔（秒）
				"value":       float64(count),           // 指标值（计数）
				"counterType": "GAUGE",                  // 计数器类型
			}

			// 解析标签字符串并添加到 point
			if tagString != "default" && tagString != "" {
				tags := ParseTagString(tagString)
				if len(tags) > 0 {
					point["tags"] = SortedTags(tags) // 标签字符串
				}
			}

			points = append(points, point)
		}

		// 如果没有标签数据，添加一个总计数点
		if len(ruleData.TagCounts) == 0 && ruleData.TotalCount > 0 {
			point := map[string]interface{}{
				"metric":      ruleName,
				"timestamp":   metrics.Timestamp,
				"step":        mm.collector.intervalSec,
				"value":       float64(ruleData.TotalCount),
				"counterType": "GAUGE",
			}
			points = append(points, point)
		}
	}

	// 如果没有规则数据但有总计数，添加一个总计数点
	if len(metrics.RuleMetrics) == 0 && metrics.TotalCount > 0 {
		point := map[string]interface{}{
			"metric":      "log.filter.total",
			"timestamp":   metrics.Timestamp,
			"step":        mm.collector.intervalSec,
			"value":       float64(metrics.TotalCount),
			"counterType": "GAUGE",
		}
		points = append(points, point)
	}

	// 如果没有任何数据点，跳过上报
	if len(points) == 0 {
		return nil
	}

	// 发送数据点数组（参考 falcon-log-agent 的批量推送方式）
	return mm.httpClient.Post(mm.apiURL, points)
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
