package filter

import (
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v2"
)

// Rule 过滤规则结构体
// 定义一条日志过滤规则，包括规则名称、匹配模式和描述
type Rule struct {
	Name        string `yaml:"name"`        // 规则名称，用于标识该规则
	Pattern     string `yaml:"pattern"`     // 正则表达式模式，用于匹配日志内容
	Description string `yaml:"description"` // 规则描述，说明该规则的用途
}

// Config 配置文件结构体
// 包含所有过滤规则的配置
type Config struct {
	Rules []Rule `yaml:"rules"` // 规则列表
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
