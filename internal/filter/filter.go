package filter

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"sync"

	"gopkg.in/yaml.v2"
)

// Rule 过滤规则结构体
// 定义一条日志过滤规则，包括规则名称、匹配模式和描述
type Rule struct {
	Name        string `yaml:"name"`        // 规则名称，用于标识该规则
	Pattern     string `yaml:"pattern"`     // 正则表达式模式，用于匹配日志内容
	Description string `yaml:"description"` // 规则描述，说明该规则的用途
}

// MatchResult 匹配结果结构体
// 包含匹配的规则和日志内容
type MatchResult struct {
	Rule    Rule   // 匹配的规则
	LogLine string // 匹配的日志行内容
}

// Config 配置文件结构体
// 包含所有过滤规则的配置
type Config struct {
	Rules []Rule `yaml:"rules"` // 规则列表
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
// 返回: 匹配结果列表（一条日志可能匹配多个规则）
func (lf *LogFilter) Match(logLine string) []MatchResult {
	lf.mu.RLock()
	defer lf.mu.RUnlock()

	var results []MatchResult

	// 遍历所有匹配器
	for i, matcher := range lf.matchers {
		if matcher.MatchString(logLine) {
			results = append(results, MatchResult{
				Rule:    lf.rules[i],
				LogLine: logLine,
			})
		}
	}

	return results
}

// Filter 过滤日志通道，将匹配的日志发送到结果通道
// logChan: 输入日志通道
// resultChan: 输出匹配结果通道
// stopChan: 停止信号通道
func (lf *LogFilter) Filter(logChan <-chan string, resultChan chan<- MatchResult, stopChan <-chan struct{}) {
	defer close(resultChan)

	for {
		select {
		case <-stopChan:
			return
		case logLine, ok := <-logChan:
			if !ok {
				// 通道已关闭
				return
			}

			// 检查日志行是否匹配规则
			results := lf.Match(logLine)
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

// LoadRules 从YAML配置文件加载过滤规则
// configPath: 配置文件路径
// 返回: 规则列表和错误信息
func LoadRules(configPath string) ([]Rule, error) {
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

	// 验证每条规则
	for i, rule := range config.Rules {
		if rule.Name == "" {
			return nil, fmt.Errorf("规则 #%d 缺少名称", i+1)
		}
		if rule.Pattern == "" {
			return nil, fmt.Errorf("规则 '%s' 缺少匹配模式", rule.Name)
		}
	}

	log.Printf("成功加载 %d 条过滤规则\n", len(config.Rules))
	return config.Rules, nil
}
