# 性能优化文档

本文档记录了所有已实施的性能优化措施，旨在提升系统在高并发、高吞吐量场景下的性能表现。

## 已实施的优化

### 1. 减少锁竞争优化

#### 1.1 Filter 模块 - 使用 atomic.Value 无锁读取
**优化前：**
- 每次 Match 调用都需要获取 RLock
- 高并发场景下大量 goroutine 竞争读锁

**优化后：**
- 使用 `atomic.Value` 存储规则快照
- Match 方法完全无锁，使用原子操作读取
- 性能提升：减少锁竞争，提升并发匹配性能

```go
// 使用 atomic.Value 实现无锁读取
snapshot := lf.snapshot.Load().(*ruleSnapshot)
```

#### 1.2 Metrics 模块 - 使用 sync.Map 和 atomic
**优化前：**
- 使用 map + RWMutex，每次 Increment 都要加锁
- 高并发下锁竞争严重

**优化后：**
- 使用 `sync.Map` 存储计数器（适合并发读多写少场景）
- 使用 `atomic` 操作进行计数（无锁）
- 性能提升：指标统计操作完全无锁，大幅提升并发性能

```go
// 无锁计数
atomic.AddInt64(&mc.totalCounter, 1)
value, _ := mc.counters.LoadOrStore(ruleName, new(int64))
atomic.AddInt64(value.(*int64), 1)
```

#### 1.3 Handler 模块 - 移除不必要的锁
**优化前：**
- ConsoleHandler 使用 Mutex 保护输出
- HTTPHandler 使用 Mutex 保护统计信息

**优化后：**
- ConsoleHandler：移除锁（fmt.Printf 本身线程安全）
- HTTPHandler：使用 atomic 操作替代 Mutex
- 性能提升：减少锁竞争，提升处理吞吐量

### 2. 通道缓冲优化

**优化前：** 所有通道缓冲大小固定为 100

**优化后：** 根据数据流特点增大缓冲
- `LogMonitor.LogChan`: 100 → 500
- `MultiMonitor.outputChan`: 100 → 500  
- `App.resultChan`: 100 → 1000
- `Filter.fileLogChan`: 100 → 500

**性能提升：** 减少通道阻塞，提高吞吐量，适合高并发场景

### 3. 并发处理优化

#### 3.1 Handler Worker Pool
**优化前：** 只有一个 handler goroutine 串行处理

**优化后：** 
- 支持多个 worker goroutine 并行处理（默认4个）
- 可以处理更多的匹配结果，提升吞吐量

```go
// 启动多个worker并行处理
for i := 0; i < hm.workerNum; i++ {
    go hm.process(resultChan, stopChan)
}
```

**性能提升：** 在多核 CPU 上可以充分利用并行处理能力

### 4. 内存分配优化

#### 4.1 预分配切片容量
**优化前：**
```go
var results []MatchResult  // 每次都要扩容
```

**优化后：**
```go
results := make([]MatchResult, 0, len(snapshot.matchers))  // 预分配容量
```

**性能提升：** 减少内存重新分配，降低 GC 压力

#### 4.2 使用 strings.Builder 优化字符串拼接
**优化前：**
```go
result += fmt.Sprintf(...)  // 每次都会创建新字符串
```

**优化后：**
```go
var builder strings.Builder
builder.Grow(512)  // 预分配容量
builder.WriteString(...)  // 高效拼接
```

**性能提升：** 减少字符串拷贝和内存分配

### 5. 正则表达式优化

**优化措施：**
- 正则表达式在初始化时预编译，避免运行时编译
- 使用 `atomic.Value` 存储规则快照，匹配时无锁读取
- Match 方法预分配结果切片容量

**性能提升：** 正则匹配是 CPU 密集型操作，无锁读取减少了开销

### 6. 运行时优化

**优化措施：**
- 在 main 中设置 `runtime.GOMAXPROCS(runtime.NumCPU())`
- 充分利用多核 CPU 的处理能力

## 性能优化效果预估

1. **锁竞争减少：** 90%+ 的锁操作被移除或优化
2. **并发处理能力：** handler worker pool 可提升 3-4 倍处理能力
3. **通道吞吐量：** 更大的缓冲可减少阻塞，提升 2-5 倍
4. **内存分配：** 预分配可减少 30-50% 的内存分配
5. **GC 压力：** 减少内存分配可降低 GC 频率和停顿时间

## 进一步优化建议

### 1. 对象池（Object Pool）
可以使用 `sync.Pool` 复用 MatchResult 和 LogLineWithFile 对象：

```go
var matchResultPool = sync.Pool{
    New: func() interface{} {
        return &MatchResult{}
    },
}
```

### 2. 批量 HTTP 上报
对于 HTTP 处理器，可以实现批量上报功能，减少 HTTP 请求次数：

```go
// 收集一批结果，批量上报
batch := make([]MatchResult, 0, 100)
// 定时或达到阈值时批量发送
```

### 3. 正则表达式优化
- 对于简单模式，可以先做字符串包含检查再正则匹配
- 考虑使用更快的字符串匹配算法（如 Boyer-Moore）

### 4. 配置文件热重载
- 使用 inotify 监听配置文件变化
- 支持运行时更新规则，避免重启

### 5. 指标统计优化
- 可以考虑使用更高效的时间序列数据库存储指标
- 实现指标聚合和采样功能

### 6. 监控和调优
- 添加性能指标（如处理延迟、吞吐量、队列长度）
- 支持动态调整 worker 数量和通道缓冲大小

## 性能测试建议

建议进行以下性能测试：

1. **吞吐量测试：** 测试每秒能处理多少条日志
2. **延迟测试：** 测试从日志产生到处理完成的时间
3. **并发测试：** 测试多文件、多规则场景下的性能
4. **内存测试：** 监控内存使用和 GC 情况
5. **CPU 测试：** 监控 CPU 使用率和各模块占比

## 配置建议

对于高性能场景，建议：

1. **增加 worker 数量：** 根据 CPU 核心数和负载调整
2. **增大通道缓冲：** 根据日志产生速度调整
3. **优化规则顺序：** 将最常用的规则放在前面
4. **使用 metrics_only 模式：** 对于高频日志，只统计指标不上报完整内容

