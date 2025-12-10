# 日志过滤监控工具

一个基于 Go 语言的日志实时监控和过滤工具，使用 [hpcloud/tail](https://github.com/hpcloud/tail) 库实现实时日志监控，支持通过正则表达式规则过滤日志内容。

## 功能特性

- ✅ **实时监控**：实时监控日志文件的变化，类似 `tail -f` 命令
- ✅ **日志轮转支持**：自动处理日志文件的轮转和移动
- ✅ **规则过滤**：支持通过正则表达式定义多条过滤规则
- ✅ **灵活配置**：通过 YAML 配置文件管理过滤规则
- ✅ **优雅退出**：支持 Ctrl+C 优雅退出

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

## 配置文件说明

配置文件使用 YAML 格式，默认文件名为 `config.yaml`。配置文件结构如下：

```yaml
rules:
  - name: "规则名称"
    pattern: "正则表达式模式"
    description: "规则描述（可选）"
```

### 配置示例

```yaml
rules:
  - name: "错误日志"
    pattern: "ERROR|FATAL|CRITICAL"
    description: "匹配包含错误、致命错误或严重错误的日志"

  - name: "特定IP访问"
    pattern: "192\\.168\\.1\\.100"
    description: "匹配来自特定IP地址的访问日志"
```

### 正则表达式提示

- 使用 `|` 表示"或"关系
- 使用 `\.` 转义点号
- 使用 `\d` 匹配数字
- 使用 `\s` 匹配空白字符

## 输出格式

当匹配到符合条件的日志时，工具会输出以下格式：

```
[2024-01-15 10:30:45] [规则: 错误日志] 2024-01-15 10:30:45 ERROR: Database connection failed
  -> 匹配包含错误、致命错误或严重错误的日志
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
    ├── monitor/           # 日志监控模块
    │   └── monitor.go
    └── filter/            # 过滤规则模块
        └── filter.go
```

## 开发

### 添加新的过滤规则

编辑 `config.yaml` 文件，添加新的规则条目：

```yaml
rules:
  - name: "新规则名称"
    pattern: "你的正则表达式"
    description: "规则说明"
```

### 代码结构说明

- `main.go`: 程序入口，处理命令行参数和信号
- `internal/monitor/monitor.go`: 日志监控核心逻辑，使用 hpcloud/tail 库
- `internal/filter/filter.go`: 配置文件解析和规则管理

## 许可证

MIT License

## 参考

- [hpcloud/tail](https://github.com/hpcloud/tail) - Go package for reading from continuously updated files
