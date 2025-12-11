# 指标统计与上报优化说明

本文档说明参考 falcon-log-agent 项目对指标统计和上报功能的优化。

## 主要优化点

### 1. 时间对齐功能（参考 falcon-log-agent 的 AlignStepTms）

**优化前：**
- 使用实际的时间戳，可能导致时间窗口不一致

**优化后：**
- 将时间戳对齐到最近的 interval 倍数
- 例如：interval=60s，tms=123 -> 对齐到 120
- 确保所有指标的时间戳在同一时间窗口内

**实现：**
```go
// AlignStepTms 将时间戳对齐到最近的步长
alignedTms := AlignStepTms(intervalSec, currentTms)
```

### 2. 按标签维度统计（参考 falcon-log-agent 的标签系统）

**优化前：**
- 只按规则名称统计，无法区分不同标签的计数

**优化后：**
- 支持按标签组合统计指标
- 使用 `SortedTags` 将标签 map 转换为排序后的字符串作为索引
- 可以按 `tag`、`log_file` 等标签维度统计

**数据结构：**
```go
type RuleMetrics struct {
    TagCounts map[string]int64 // tagstring -> count
}
```

**示例：**
- 规则 "错误日志" 有 tag="error", log_file="/var/log/app.log"
- 统计时会分别计数：`tag=error,log_file=/var/log/app.log` -> count

### 3. 优化的指标上报格式（参考 falcon-log-agent 的 FalconPoint）

**优化前：**
- 简单的 JSON 对象，包含总计数和规则计数

**优化后：**
- 转换为点格式数组，每个点包含：
  - `metric`: 指标名称（规则名称）
  - `timestamp`: 时间戳（已对齐）
  - `step`: 统计间隔（秒）
  - `value`: 指标值（计数）
  - `counterType`: 计数器类型（GAUGE）
  - `tags`: 标签字符串（可选）

**上报格式示例：**
```json
[
  {
    "metric": "错误日志",
    "timestamp": 1234567800,
    "step": 60,
    "value": 100,
    "counterType": "GAUGE",
    "tags": "log_file=/var/log/app.log,tag=error"
  },
  {
    "metric": "警告日志",
    "timestamp": 1234567800,
    "step": 60,
    "value": 50,
    "counterType": "GAUGE",
    "tags": "log_file=/var/log/app.log,tag=warning"
  }
]
```

### 4. 标签工具函数（参考 falcon-log-agent 的 utils）

**新增功能：**
- `SortedTags`: 将标签 map 转换为排序后的字符串
- `ParseTagString`: 解析标签字符串为 map
- `AlignStepTms`: 时间戳对齐函数

## 数据结构优化

### 新的 Metrics 结构

```go
type Metrics struct {
    Timestamp   int64                        // 时间戳（对齐后的）
    RuleMetrics map[string]*RuleMetricsData  // 每个规则的指标（按标签维度）
    TotalCount  int64                        // 总计数
    Duration    int64                        // 统计时长（秒）
    RuleCounts  map[string]int64            // 向后兼容字段
}

type RuleMetricsData struct {
    RuleName   string            // 规则名称
    TagCounts  map[string]int64  // 按标签维度的计数
    TotalCount int64             // 该规则的总计数
}
```

### 指标收集优化

**优化前：**
```go
counters sync.Map // key: ruleName, value: *int64
```

**优化后：**
```go
ruleMetrics sync.Map // key: ruleName, value: *RuleMetrics
// RuleMetrics 包含 TagCounts map[string]int64
```

## 使用示例

### 配置示例

```yaml
metrics:
  enabled: true
  interval: 1m
  api_url: http://localhost:8080/api/v1/metrics  # 指标上报地址
  timeout: 10s
```

### 指标统计输出示例

```
========== 指标统计 [2024-01-01 10:00:00] ==========
统计时长: 60 秒
总匹配数: 150
各规则匹配数（按标签维度）:
  - 错误日志 (总计: 100):
    * [log_file=/var/log/app.log,tag=error]: 100
  - 警告日志 (总计: 50):
    * [log_file=/var/log/app.log,tag=warning]: 50
==========================================
```

## 性能优化

1. **无锁统计**：使用 `sync.Map` 和 `atomic` 操作，减少锁竞争
2. **标签索引**：使用排序后的标签字符串作为索引，查找效率高
3. **时间对齐**：统一时间窗口，便于聚合和分析

## 与 falcon-log-agent 的对比

| 特性 | falcon-log-agent | 本项目（优化后） |
|------|------------------|------------------|
| 时间对齐 | ✅ AlignStepTms | ✅ 已实现 |
| 标签维度统计 | ✅ 支持 | ✅ 已实现 |
| 标签排序 | ✅ SortedTags | ✅ 已实现 |
| 批量推送 | ✅ 队列批量 | ✅ 数组批量 |
| 延迟推送 | ✅ 支持乱序 | ⏳ 待实现 |
| 聚合函数 | ✅ cnt/avg/sum/max/min | ⏳ 当前仅 cnt |

## 未来优化方向

1. **支持更多聚合函数**：avg, sum, max, min（参考 falcon-log-agent）
2. **延迟推送机制**：解决日志时间戳乱序问题
3. **批量推送优化**：使用队列和批量发送，提高吞吐量
4. **缓存机制**：缓存已推送的指标，避免重复上报

