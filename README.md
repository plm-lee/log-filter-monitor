# 日志过滤监控工具

一个基于 Go 语言的日志实时监控和过滤工具，采用模块化设计，使用 [hpcloud/tail](https://github.com/hpcloud/tail) 库实现实时日志监控，支持通过正则表达式规则过滤日志内容。

## 功能特性

- ✅ **实时监控**：实时监控日志文件的变化，类似 `tail -f` 命令
- ✅ **日志轮转支持**：自动处理日志文件的轮转和移动
- ✅ **规则过滤**：支持通过正则表达式定义多条过滤规则
- ✅ **灵活配置**：通过 YAML 配置文件管理过滤规则
- ✅ **模块化设计**：监控、过滤、处理三个模块独立，职责清晰
- ✅ **多种处理方式**：支持控制台输出和 HTTP 接口上报
- ✅ **指标统计**：每分钟自动统计匹配日志数量（参考 falcon-log-agent）
- ✅ **优雅退出**：支持 Ctrl+C 优雅退出

## 架构设计

项目采用模块化架构，分为四个独立模块：

1. **监控模块（monitor）**：负责读取日志文件，通过 channel 发送日志行
2. **过滤模块（filter）**：负责根据规则匹配日志，通过 channel 发送匹配结果
3. **处理模块（handler）**：负责处理匹配到的日志，支持控制台输出和 HTTP 上报
4. **指标模块（metrics）**：负责统计匹配日志数量，定期输出统计信息（参考 falcon-log-agent）

## 安装

### 前置要求

- Go 1.23 或更高版本

### 安装依赖

```bash
go mod download
```

## 使用方法

### 基本用法

```bash
go run main.go -file /path/to/your/logfile.log
```

### 指定配置文件

```bash
go run main.go -file /path/to/your/logfile.log -config custom-config.yaml
```

### 编译后使用

```bash
# 编译
go build -o log-filter-monitor

# 运行
./log-filter-monitor -file /path/to/your/logfile.log
```

### 命令行参数说明

- `-file`：要监控的日志文件路径（必需）
- `-config`：配置文件路径（可选，默认：config.yaml）

**注意**：处理器类型、API 地址和超时时间现在都在配置文件中配置，不再通过命令行参数指定。

## 配置文件说明

配置文件使用 YAML 格式，默认文件名为 `config.yaml`。配置文件包含两部分：处理器配置和过滤规则配置。

### 配置文件结构

```yaml
# 处理器配置
handler:
  type: console # 处理器类型：console（控制台输出）或 http（HTTP上报）
  api_url: "" # HTTP上报接口地址（当type为http时必需）
  timeout: "10s" # HTTP请求超时时间（可选，默认：10s，支持单位：s、m、h）

# 过滤规则配置
rules:
  - name: "规则名称"
    pattern: "正则表达式模式"
    description: "规则描述（可选）"
```

### 配置示例

#### 使用控制台输出（默认）

```yaml
# 处理器配置
handler:
  type: console

# 指标统计配置
metrics:
  enabled: true
  interval: 1m

# 过滤规则
rules:
  - name: "错误日志"
    pattern: "ERROR|FATAL|CRITICAL|Exception"
    description: "匹配包含错误、致命错误、严重错误或异常的日志"

  - name: "警告日志"
    pattern: "WARN|WARNING"
    description: "匹配包含警告信息的日志"
```

#### 使用 HTTP 接口上报

```yaml
# 处理器配置
handler:
  type: http
  api_url: http://your-api-endpoint.com/logs
  timeout: 30s

# 指标统计配置
metrics:
  enabled: true
  interval: 1m

# 过滤规则
rules:
  - name: "错误日志"
    pattern: "ERROR|FATAL|CRITICAL|Exception"
    description: "匹配包含错误、致命错误、严重错误或异常的日志"

  - name: "特定IP访问"
    pattern: "192\\.168\\.1\\.(100|101|102)"
    description: "匹配来自特定IP地址的访问日志"

  - name: "数据库操作"
    pattern: "(SELECT|INSERT|UPDATE|DELETE).*FROM|database|Database|DB"
    description: "匹配数据库相关操作的日志"

  - name: "HTTP错误状态码"
    pattern: "HTTP/1\\.\\d\"\\s+(4\\d{2}|5\\d{2})"
    description: "匹配HTTP 4xx和5xx错误状态码"
```

### 正则表达式提示

- 使用 `|` 表示"或"关系
- 使用 `\.` 转义点号
- 使用 `\d` 匹配数字
- 使用 `\s` 匹配空白字符

## 输出格式

### 控制台输出

当匹配到符合条件的日志时，工具会输出以下格式：

```
[2024-01-15 10:30:45] [规则: 错误日志] 2024-01-15 10:30:45 ERROR: Database connection failed
  -> 匹配包含错误、致命错误或严重错误的日志
```

### 指标统计输出

系统会按照配置的间隔（默认每分钟）自动输出统计信息：

```
========== 指标统计 [2024-01-15 10:31:00] ==========
统计时长: 60 秒
总匹配数: 15
各规则匹配数:
  - 错误日志: 10
  - 警告日志: 5
==========================================
```

### HTTP 上报格式

当使用 HTTP 处理器时，匹配的日志会以 JSON 格式上报到指定接口：

```json
{
  "timestamp": 1705294245,
  "rule_name": "错误日志",
  "rule_desc": "匹配包含错误、致命错误或严重错误的日志",
  "log_line": "2024-01-15 10:30:45 ERROR: Database connection failed",
  "pattern": "ERROR|FATAL|CRITICAL"
}
```

## 项目结构

```
log-filter-monitor/
├── main.go                 # 主程序入口
├── config.yaml            # 默认配置文件
├── go.mod                 # Go模块文件
├── go.sum                 # 依赖校验文件
├── README.md              # 项目说明文档
└── internal/
    ├── monitor/           # 日志监控模块（只负责读取日志）
    │   └── monitor.go
    ├── filter/            # 日志过滤模块（负责规则匹配）
    │   └── filter.go
    ├── handler/           # 日志处理模块（负责输出和上报）
    │   └── handler.go
    └── metrics/           # 指标统计模块（负责统计和上报）
        └── metrics.go
```

## 开发

### 模块说明

#### 监控模块（monitor）

负责读取日志文件，通过 `LogChan` channel 输出日志行。

```go
logMonitor := monitor.NewLogMonitor(logFile)
logMonitor.Start()
// 从 logMonitor.LogChan 读取日志行
```

#### 过滤模块（filter）

负责根据规则匹配日志，支持：

- 加载规则配置
- 编译正则表达式
- 匹配日志行
- 更新规则（支持热更新）

```go
logFilter, _ := filter.NewLogFilter(rules)
// 匹配单条日志
results := logFilter.Match(logLine)
// 或使用 Filter 方法处理 channel
logFilter.Filter(logChan, resultChan, stopChan)
```

#### 处理模块（handler）

支持多种处理器：

- `ConsoleHandler`：控制台输出
- `HTTPHandler`：HTTP 接口上报
- `MultiHandler`：组合多个处理器

```go
// 控制台输出
consoleHandler := handler.NewConsoleHandler()

// HTTP上报
httpHandler := handler.NewHTTPHandler(apiURL, timeout)

// 组合使用
multiHandler := handler.NewMultiHandler(consoleHandler, httpHandler)
```

#### 指标模块（metrics）

负责统计和上报指标：

- 按规则统计匹配数量
- 定期输出统计信息
- 支持自定义统计间隔

```go
// 创建指标收集器
metricsCollector := metrics.NewMetricsCollector(1 * time.Minute)
metricsCollector.Start(metrics.LogOutputFunc)

// 统计匹配结果
metricsCollector.IncrementByMatchResult(matchResult)
```

### 配置 HTTP 上报

编辑 `config.yaml` 文件，修改 handler 配置：

```yaml
handler:
  type: http
  api_url: http://your-api-endpoint.com/logs
  timeout: 30s
```

### 添加新的过滤规则

编辑 `config.yaml` 文件，在 rules 部分添加新的规则条目：

```yaml
rules:
  - name: "新规则名称"
    pattern: "你的正则表达式"
    description: "规则说明"
```

### 添加自定义处理器

实现 `handler.LogHandler` 接口：

```go
type MyHandler struct {}

func (h *MyHandler) Handle(matchResult filter.MatchResult) error {
    // 处理逻辑
    return nil
}
```

## 许可证

MIT License

## 参考

- [hpcloud/tail](https://github.com/hpcloud/tail) - Go package for reading from continuously updated files
