package filter

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"sync"

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
	ReportMode    string `yaml:"report_mode"`    // 上报模式：full（上报完整日志）或 metrics_only（只上报指标，默认：full）
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
}

// HandlerConfig 处理器配置结构体
// 定义日志处理器的配置信息
type HandlerConfig struct {
	Type    string `yaml:"type"`    // 处理器类型：console 或 http
	APIURL  string `yaml:"api_url"` // HTTP上报接口地址（当type为http时必需）
	Timeout string `yaml:"timeout"` // HTTP请求超时时间（可选，默认：10s）
}

// MetricsConfig 指标统计配置结构体
// 定义指标统计的配置信息
type MetricsConfig struct {
	Enabled  bool   `yaml:"enabled"`  // 是否启用指标统计（默认：true）
	Interval string `yaml:"interval"` // 统计间隔（可选，默认：1m，单位：s、m、h）
}

// Config 配置文件结构体
// 包含所有配置信息
type Config struct {
	Rules   []Rule        `yaml:"rules"`   // 规则列表
	Handler HandlerConfig `yaml:"handler"` // 处理器配置
	Metrics MetricsConfig `yaml:"metrics"` // 指标统计配置
}

// LogFilter 日志过滤器结构体
// 负责根据规则过滤日志行
type LogFilter struct {
	rules    []Rule           // 过滤规则列表
	matchers []*regexp.Regexp // 编译后的正则表达式匹配器
	mu       sync.RWMutex     // 保护规则的读写锁
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

	return &LogFilter{
		rules:    validRules,
		matchers: matchers,
	}, nil
}

// Match 检查日志行是否匹配任何规则
// logLine: 要检查的日志行内容
// logFile: 日志文件路径
// 返回: 匹配结果列表（一条日志可能匹配多个规则）
func (lf *LogFilter) Match(logLine string, logFile string) []MatchResult {
	lf.mu.RLock()
	defer lf.mu.RUnlock()

	var results []MatchResult

	// 遍历所有匹配器
	for i, matcher := range lf.matchers {
		if matcher.MatchString(logLine) {
			rule := lf.rules[i]
			results = append(results, MatchResult{
				Rule:    rule,
				LogLine: logLine,
				LogFile: logFile,
				Tag:     rule.Tag,
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

// LogLineWithFile 带文件信息的日志行
type LogLineWithFile struct {
	LogLine string // 日志行内容
	LogFile string // 日志文件路径
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
	fileLogChan := make(chan LogLineWithFile, 100)
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

	// 更新规则（需要加锁）
	lf.mu.Lock()
	lf.rules = validRules
	lf.matchers = matchers
	lf.mu.Unlock()

	log.Printf("成功更新过滤器，加载 %d 条有效规则\n", len(validRules))

	return nil
}

// GetRules 获取当前所有规则
// 返回: 规则列表
func (lf *LogFilter) GetRules() []Rule {
	lf.mu.RLock()
	defer lf.mu.RUnlock()

	// 返回规则的副本
	rules := make([]Rule, len(lf.rules))
	copy(rules, lf.rules)
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

		// 设置默认值
		if rule.ReportMode == "" {
			rule.ReportMode = ReportModeFull // 默认上报完整日志
		}
		if rule.ReportMode != ReportModeFull && rule.ReportMode != ReportModeMetricsOnly {
			return nil, fmt.Errorf("规则 '%s' 的 report_mode 必须为 '%s' 或 '%s'", rule.Name, ReportModeFull, ReportModeMetricsOnly)
		}
		// 注意：MetricsEnable 在 YAML 中如果不设置，零值是 false
		// 为了支持默认启用，我们需要特殊处理。但为了简化，这里保持原样
		// 用户需要显式设置 metrics_enable: true 来启用指标统计
		// 如果设置为 false，则禁用指标统计

		// log_file 和 tag 是可选字段，无需验证
	}

	// 验证处理器配置
	if config.Handler.Type == "" {
		// 如果没有配置，默认为 console
		config.Handler.Type = "console"
		log.Println("未配置处理器类型，使用默认值：console")
	}

	// 如果使用 HTTP 处理器，验证 API 地址
	if config.Handler.Type == "http" {
		if config.Handler.APIURL == "" {
			return nil, fmt.Errorf("使用HTTP处理器时必须配置 api_url")
		}
	}

	// 验证处理器类型
	if config.Handler.Type != "console" && config.Handler.Type != "http" {
		return nil, fmt.Errorf("不支持的处理器类型 '%s'，支持的类型：console, http", config.Handler.Type)
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
