package filter

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"sync"
	"sync/atomic"

	"gopkg.in/yaml.v2"
)

const (
	// ReportModeFull 上报模式：上报完整日志
	ReportModeFull = "full"
	// ReportModeMetricsOnly 上报模式：只上报指标统计，不上报完整日志
	ReportModeMetricsOnly = "metrics_only"
)

// Rule 过滤规则结构体
// 定义一条日志过滤规则，包括规则名称、匹配模式和描述
type Rule struct {
	Name          string `yaml:"name"`           // 规则名称，用于标识该规则
	Pattern       string `yaml:"pattern"`        // 正则表达式模式，用于匹配日志内容
	Description   string `yaml:"description"`    // 规则描述，说明该规则的用途
	LogFile       string `yaml:"log_file"`       // 要监控的日志文件路径（可选，如果未设置则使用全局文件）
	Tag           string `yaml:"tag"`            // 标签，用于标识该规则（可选，会在打印和上报时带上）
	MetricsEnable *bool  `yaml:"metrics_enable"` // 是否启用指标统计（指针类型，nil表示使用全局配置，true/false表示显式设置）
	ReportMode    string `yaml:"report_mode"`    // 上报模式：full（完整日志）或 metrics_only（只上报指标，不上报完整日志）
}

// IsReportModeMetricsOnly 检查是否为仅指标上报模式
// 返回: 若 report_mode 为 metrics_only 则 true，否则 false
func (r *Rule) IsReportModeMetricsOnly() bool {
	return r.ReportMode == ReportModeMetricsOnly
}

// IsMetricsEnabled 检查是否启用指标统计
// globalEnabled: 全局指标统计是否启用
// 返回: 是否启用指标统计
func (r *Rule) IsMetricsEnabled(globalEnabled bool) bool {
	if r.MetricsEnable == nil {
		// 如果规则未显式设置，使用全局配置
		return globalEnabled
	}
	return *r.MetricsEnable
}

// MatchResult 匹配结果结构体
// 包含匹配的规则和日志内容
type MatchResult struct {
	Rule    Rule   // 匹配的规则
	LogLine string // 匹配的日志行内容
	LogFile string // 日志文件路径
	Tag     string // 标签
	Offset  int64  // 文件内字节偏移（用于断点续传）
}

// CheckpointConfig 断点续传配置
type CheckpointConfig struct {
	Enabled bool   `yaml:"enabled"` // 是否启用断点续传（仅 HTTP 批量模式有效）
	Path    string `yaml:"path"`    // 检查点文件路径，默认 ~/.log-agent/checkpoint.json
}

// HandlerConfig 处理器配置结构体
// 定义日志处理器的配置信息
type HandlerConfig struct {
	Type            string `yaml:"type"`              // 处理器类型：console、http、udp 或 tcp
	APIURL          string `yaml:"api_url"`           // HTTP上报接口地址（当type为http时必需）
	Timeout         string `yaml:"timeout"`           // HTTP请求超时时间（可选，默认：10s）
	BatchEnabled    *bool  `yaml:"batch_enabled"`     // 是否启用批量上报（nil/true=启用，false=逐条，默认启用以支撑高吞吐）
	BatchSize       int    `yaml:"batch_size"`        // 每批条数（可选，默认：100，最大100）
	BatchInterval   string `yaml:"batch_interval"`    // 批量刷新间隔（可选，默认：1s）
	RetryCount      int    `yaml:"retry_count"`       // 失败重试次数（可选，默认：3）
	RetryBaseDelay  string `yaml:"retry_base_delay"`  // 重试基础延迟（可选，默认：1s）
	WorkerNum       int    `yaml:"worker_num"`        // handler worker 数量（0=默认4，高吞吐场景可调大）
	UDPAddr         string `yaml:"udp_addr"`          // UDP 目标地址（当type为udp时必需，格式：host:port）
	UDPSecret       string `yaml:"udp_secret"`        // UDP 认证密钥（可选，与 log-manager udp.secret 一致时做校验）
	TCPAddr         string `yaml:"tcp_addr"`          // TCP 目标地址（当type为tcp时必需，格式：host:port）
	TCPSecret       string `yaml:"tcp_secret"`        // TCP 认证密钥（可选，与 log-manager tcp.secret 一致时做校验）
	TCPBatchSize    int    `yaml:"tcp_batch_size"`    // TCP 批量条数（可选，默认：50）
	TCPFlushInterval string `yaml:"tcp_flush_interval"` // TCP 批量刷新间隔（可选，默认：1s）
}

// MetricsConfig 指标统计配置结构体
// 定义指标统计的配置信息
type MetricsConfig struct {
	Enabled  bool   `yaml:"enabled"`  // 是否启用指标统计（默认：true）
	Interval string `yaml:"interval"` // 统计间隔（可选，默认：1m，单位：s、m、h）
	APIURL   string `yaml:"api_url"`  // 指标上报API地址（可选，如果配置则会上报到HTTP接口）
	Timeout  string `yaml:"timeout"`  // HTTP请求超时时间（可选，默认：10s）
}

// ConfigPullConfig 配置拉取配置（从 Manager 定时拉取并热更新规则）
type ConfigPullConfig struct {
	Enabled  bool   `yaml:"enabled"`   // 是否启用配置拉取
	URL      string `yaml:"url"`       // 拉取地址，如 http://manager/log/manager/api/v1/agent/config
	Interval string `yaml:"interval"`  // 拉取间隔，如 30s
	AgentID  string `yaml:"agent_id"`  // agent_id 参数，默认 default
	APIKey   string `yaml:"api_key"`   // 可选，与 Manager api_key 一致时做认证
}

// Config 配置文件结构体
// 包含所有配置信息
type Config struct {
	Rules      []Rule           `yaml:"rules"`       // 规则列表
	Handler    HandlerConfig    `yaml:"handler"`     // 处理器配置
	Metrics    MetricsConfig    `yaml:"metrics"`     // 指标统计配置
	Checkpoint CheckpointConfig `yaml:"checkpoint"`  // 断点续传配置
	ConfigPull ConfigPullConfig `yaml:"config_pull"` // 配置拉取（热更新规则）
}

// ruleSnapshot 规则快照结构
// 用于无锁读取规则
type ruleSnapshot struct {
	rules    []Rule           // 过滤规则列表
	matchers []*regexp.Regexp // 编译后的正则表达式匹配器
}

// LogFilter 日志过滤器结构体
// 负责根据规则过滤日志行
type LogFilter struct {
	snapshot atomic.Value // 规则快照，用于无锁读取
	mu       sync.Mutex   // 保护规则更新的互斥锁
}

// NewLogFilter 创建新的日志过滤器实例
// rules: 过滤规则列表
// 返回: LogFilter实例和错误信息
func NewLogFilter(rules []Rule) (*LogFilter, error) {
	// 编译所有规则的正则表达式
	matchers := make([]*regexp.Regexp, 0, len(rules))
	validRules := make([]Rule, 0, len(rules))

	for _, rule := range rules {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			log.Printf("警告：规则 '%s' 的正则表达式编译失败: %v，将跳过此规则", rule.Name, err)
			continue
		}
		matchers = append(matchers, re)
		validRules = append(validRules, rule)
	}

	// 检查是否有有效的规则
	if len(matchers) == 0 {
		return nil, fmt.Errorf("没有有效的过滤规则，所有规则的正则表达式编译都失败了")
	}

	log.Printf("成功初始化过滤器，加载 %d 条有效规则\n", len(validRules))

	lf := &LogFilter{}
	snapshot := &ruleSnapshot{
		rules:    validRules,
		matchers: matchers,
	}
	lf.snapshot.Store(snapshot)

	return lf, nil
}

// Match 检查日志行是否匹配任何规则
// logLine: 要检查的日志行内容
// logFile: 日志文件路径
// 返回: 匹配结果列表（一条日志可能匹配多个规则）
func (lf *LogFilter) Match(logLine string, logFile string) []MatchResult {
	// 使用原子值无锁读取规则快照
	snapshot := lf.snapshot.Load().(*ruleSnapshot)

	// 预分配容量，假设最多匹配所有规则（实际通常更少）
	results := make([]MatchResult, 0, len(snapshot.matchers))

	// 遍历所有匹配器
	for i, matcher := range snapshot.matchers {
		if matcher.MatchString(logLine) {
			rule := snapshot.rules[i]
			results = append(results, MatchResult{
				Rule:    rule,
				LogLine: logLine,
				LogFile: logFile,
				Tag:     rule.Tag,
				Offset:  0,
			})
		}
	}

	return results
}

// FilterManager 过滤器管理器
// 负责管理日志过滤器的运行
type FilterManager struct {
	filter *LogFilter
	wg     sync.WaitGroup
}

// NewFilterManager 创建过滤器管理器
// filter: 日志过滤器
// 返回: FilterManager实例
func NewFilterManager(filter *LogFilter) *FilterManager {
	return &FilterManager{
		filter: filter,
	}
}

// Start 启动过滤器
// logChan: 输入日志通道（带文件信息）
// resultChan: 输出匹配结果通道
// stopChan: 停止信号通道
func (fm *FilterManager) Start(logChan <-chan LogLineWithFile, resultChan chan<- MatchResult, stopChan <-chan struct{}) {
	fm.wg.Add(1)
	go func() {
		defer fm.wg.Done()
		defer close(resultChan)
		fm.filter.filter(logChan, resultChan, stopChan)
	}()
}

// Wait 等待过滤器完成
func (fm *FilterManager) Wait() {
	fm.wg.Wait()
}

// UpdateRules 热更新规则（供配置拉取使用）
func (fm *FilterManager) UpdateRules(rules []Rule) error {
	return fm.filter.UpdateRules(rules)
}

// LogLineWithFile 带文件信息的日志行
type LogLineWithFile struct {
	LogLine string // 日志行内容
	LogFile string // 日志文件路径
	Offset  int64  // 文件内字节偏移（用于断点续传）
}

// filter 过滤日志通道，将匹配的日志发送到结果通道（内部方法）
// logChan: 输入日志通道（带文件信息）
// resultChan: 输出匹配结果通道
// stopChan: 停止信号通道
func (lf *LogFilter) filter(logChan <-chan LogLineWithFile, resultChan chan<- MatchResult, stopChan <-chan struct{}) {
	for {
		select {
		case <-stopChan:
			return
		case logLineWithFile, ok := <-logChan:
			if !ok {
				// 通道已关闭
				return
			}

			// 检查日志行是否匹配规则
			results := lf.Match(logLineWithFile.LogLine, logLineWithFile.LogFile)
			for i := range results {
				results[i].Offset = logLineWithFile.Offset
			}
			for _, result := range results {
				select {
				case resultChan <- result:
				case <-stopChan:
					return
				}
			}
		}
	}
}

// Filter 过滤日志通道，将匹配的日志发送到结果通道（保留向后兼容，使用空字符串作为文件路径）
// logChan: 输入日志通道（字符串格式）
// resultChan: 输出匹配结果通道
// stopChan: 停止信号通道
func (lf *LogFilter) Filter(logChan <-chan string, resultChan chan<- MatchResult, stopChan <-chan struct{}) {
	defer close(resultChan)

	// 转换 channel 类型
	const fileLogChanSize = 500 // 增加缓冲大小，提高性能
	fileLogChan := make(chan LogLineWithFile, fileLogChanSize)
	go func() {
		defer close(fileLogChan)
		for {
			select {
			case <-stopChan:
				return
			case logLine, ok := <-logChan:
				if !ok {
					return
				}
				select {
				case fileLogChan <- LogLineWithFile{LogLine: logLine, LogFile: ""}:
				case <-stopChan:
					return
				}
			}
		}
	}()

	lf.filter(fileLogChan, resultChan, stopChan)
}

// UpdateRules 更新过滤规则
// rules: 新的规则列表
// 返回: 错误信息（如果有）
func (lf *LogFilter) UpdateRules(rules []Rule) error {
	// 编译新的规则
	matchers := make([]*regexp.Regexp, 0, len(rules))
	validRules := make([]Rule, 0, len(rules))

	for _, rule := range rules {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			log.Printf("警告：规则 '%s' 的正则表达式编译失败: %v，将跳过此规则", rule.Name, err)
			continue
		}
		matchers = append(matchers, re)
		validRules = append(validRules, rule)
	}

	if len(matchers) == 0 {
		return fmt.Errorf("没有有效的过滤规则")
	}

	// 更新规则快照（需要加锁）
	lf.mu.Lock()
	snapshot := &ruleSnapshot{
		rules:    validRules,
		matchers: matchers,
	}
	lf.snapshot.Store(snapshot)
	lf.mu.Unlock()

	log.Printf("成功更新过滤器，加载 %d 条有效规则\n", len(validRules))

	return nil
}

// GetRules 获取当前所有规则
// 返回: 规则列表
func (lf *LogFilter) GetRules() []Rule {
	snapshot := lf.snapshot.Load().(*ruleSnapshot)

	// 返回规则的副本
	rules := make([]Rule, len(snapshot.rules))
	copy(rules, snapshot.rules)
	return rules
}

// LoadConfig 从YAML配置文件加载完整配置
// configPath: 配置文件路径
// 返回: 配置信息和错误信息
func LoadConfig(configPath string) (*Config, error) {
	// 读取配置文件内容
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("无法读取配置文件 %s: %w", configPath, err)
	}

	// 解析YAML配置
	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("无法解析配置文件 %s: %w", configPath, err)
	}

	// 验证规则
	if len(config.Rules) == 0 {
		return nil, fmt.Errorf("配置文件中没有定义任何规则")
	}

	// 验证每条规则并设置默认值
	for i := range config.Rules {
		rule := &config.Rules[i]
		if rule.Name == "" {
			return nil, fmt.Errorf("规则 #%d 缺少名称", i+1)
		}
		if rule.Pattern == "" {
			return nil, fmt.Errorf("规则 '%s' 缺少匹配模式", rule.Name)
		}
	}

	// 验证处理器配置（默认 tcp 长连接，可靠+高性能）
	if config.Handler.Type == "" {
		config.Handler.Type = "tcp"
		log.Println("未配置处理器类型，使用默认值：tcp（TCP 长连接）")
	}
	if config.Handler.Type == "tcp" && config.Handler.TCPAddr == "" {
		config.Handler.TCPAddr = "127.0.0.1:8890"
		log.Println("未配置 tcp_addr，使用默认值：127.0.0.1:8890")
	}

	// 如果使用 HTTP 处理器，验证 API 地址
	if config.Handler.Type == "http" {
		if config.Handler.APIURL == "" {
			return nil, fmt.Errorf("使用HTTP处理器时必须配置 api_url")
		}
	}

	// 如果没有配置 metrics，默认启用
	if config.Metrics.Interval == "" {
		config.Metrics.Interval = "1m"
	}

	log.Printf("成功加载配置 - 规则数: %d, 处理器类型: %s, 指标统计: %v\n",
		len(config.Rules), config.Handler.Type, config.Metrics.Enabled)
	return &config, nil
}

// LoadRules 从YAML配置文件加载过滤规则（兼容旧版本）
// configPath: 配置文件路径
// 返回: 规则列表和错误信息
// 注意：此函数已废弃，建议使用 LoadConfig
func LoadRules(configPath string) ([]Rule, error) {
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	return config.Rules, nil
}
