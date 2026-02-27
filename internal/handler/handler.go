package handler

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

// LogHandler 日志处理器接口
// 定义处理匹配日志的统一接口
type LogHandler interface {
	// Handle 处理匹配结果
	// matchResult: 匹配结果
	Handle(matchResult filter.MatchResult) error
}

// ConsoleHandler 控制台输出处理器
// 将匹配的日志输出到控制台
// 注意：fmt.Printf 是线程安全的，不需要额外加锁
type ConsoleHandler struct {
	// 移除互斥锁，fmt.Printf 本身是线程安全的，加锁会影响性能
}

// NewConsoleHandler 创建控制台输出处理器
// 返回: ConsoleHandler实例
func NewConsoleHandler() *ConsoleHandler {
	return &ConsoleHandler{}
}

// Handle 处理匹配结果，输出到控制台
// matchResult: 匹配结果
// 返回: 错误信息（如果有）
func (h *ConsoleHandler) Handle(matchResult filter.MatchResult) error {
	// fmt.Printf 是线程安全的，不需要加锁
	timestamp := time.Now().Format("2006-01-02 15:04:05")

	// 优化字符串构建
	if matchResult.Tag != "" {
		fmt.Printf("[%s] [规则: %s] [标签: %s] %s\n",
			timestamp, matchResult.Rule.Name, matchResult.Tag, matchResult.LogLine)
	} else {
		fmt.Printf("[%s] [规则: %s] %s\n",
			timestamp, matchResult.Rule.Name, matchResult.LogLine)
	}

	// 如果规则有描述，也输出描述信息
	if matchResult.Rule.Description != "" {
		fmt.Printf("  -> %s\n", matchResult.Rule.Description)
	}

	return nil
}

// HTTPHandler HTTP接口上报处理器
// 将匹配的日志通过HTTP接口上报
type HTTPHandler struct {
	apiURL  string        // API接口地址
	client  HTTPClient    // HTTP客户端接口
	timeout time.Duration // 请求超时时间
	mu      sync.Mutex    // 保护统计信息的互斥锁
	success int64         // 成功上报次数
	failed  int64         // 失败上报次数
}

// HTTPClient HTTP客户端接口
// 用于抽象HTTP请求，方便测试和扩展
type HTTPClient interface {
	Post(url string, data interface{}) error
}

// defaultHTTPClient 默认HTTP客户端实现
type defaultHTTPClient struct {
	client  *http.Client  // HTTP客户端
	timeout time.Duration // 请求超时时间
}

// NewDefaultHTTPClient 创建默认HTTP客户端
// 使用连接池复用 TCP 连接，支撑高吞吐
func NewDefaultHTTPClient(timeout time.Duration) *defaultHTTPClient {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 50,
		IdleConnTimeout:     90 * time.Second,
	}
	return &defaultHTTPClient{
		client: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
		timeout: timeout,
	}
}

// Post 发送POST请求
// url: 请求地址
// data: 请求数据（会被序列化为JSON）
// 返回: 错误信息（如果有）
func (c *defaultHTTPClient) Post(url string, data interface{}) error {
	// 将数据序列化为JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("序列化数据失败: %w", err)
	}

	// 创建POST请求
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("创建HTTP请求失败: %w", err)
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")

	// 发送请求
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("发送HTTP请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP请求失败，状态码: %d", resp.StatusCode)
	}

	return nil
}

// NewHTTPHandler 创建HTTP接口上报处理器
// apiURL: API接口地址
// timeout: 请求超时时间
// 返回: HTTPHandler实例
func NewHTTPHandler(apiURL string, timeout time.Duration) *HTTPHandler {
	return &HTTPHandler{
		apiURL:  apiURL,
		client:  NewDefaultHTTPClient(timeout),
		timeout: timeout,
	}
}

// NewHTTPHandlerWithClient 使用自定义HTTP客户端创建HTTP接口上报处理器
// apiURL: API接口地址
// timeout: 请求超时时间
// client: HTTP客户端
// 返回: HTTPHandler实例
func NewHTTPHandlerWithClient(apiURL string, timeout time.Duration, client HTTPClient) *HTTPHandler {
	return &HTTPHandler{
		apiURL:  apiURL,
		client:  client,
		timeout: timeout,
	}
}

// Handle 处理匹配结果，通过HTTP接口上报
// matchResult: 匹配结果
// 返回: 错误信息（如果有）
func (h *HTTPHandler) Handle(matchResult filter.MatchResult) error {
	// 构建上报数据
	data := map[string]interface{}{
		"timestamp": time.Now().Unix(),
		"rule_name": matchResult.Rule.Name,
		"rule_desc": matchResult.Rule.Description,
		"log_line":  matchResult.LogLine,
		"log_file":  matchResult.LogFile,
		"pattern":   matchResult.Rule.Pattern,
	}

	// 如果设置了标签，添加到上报数据中
	if matchResult.Tag != "" {
		data["tag"] = matchResult.Tag
	}

	// 发送HTTP请求
	err := h.client.Post(h.apiURL, data)
	if err != nil {
		atomic.AddInt64(&h.failed, 1)
		log.Printf("HTTP上报失败: %v\n", err)
		return err
	}

	atomic.AddInt64(&h.success, 1)
	return nil
}

// GetStats 获取统计信息
// 返回: 成功次数和失败次数
func (h *HTTPHandler) GetStats() (success int64, failed int64) {
	return atomic.LoadInt64(&h.success), atomic.LoadInt64(&h.failed)
}

// CheckpointSaver 检查点保存接口（ACK 后调用）
type CheckpointSaver interface {
	SaveMax(filePath string, offset int64) error
}

// BatchHTTPHandler 批量HTTP上报处理器
// 缓冲匹配结果，按条数或定时批量发送到 POST /api/v1/logs/batch
type BatchHTTPHandler struct {
	apiURL        string
	batchURL      string
	client        HTTPClient
	timeout       time.Duration
	batchSize     int
	flushInterval time.Duration
	retryCount    int           // 失败重试次数
	retryDelay    time.Duration // 重试基础延迟（指数退避）
	buffer        []filter.MatchResult
	mu            sync.Mutex
	success       int64
	failed        int64
	stopChan      chan struct{}
	wg            sync.WaitGroup
	checkpoint    CheckpointSaver
}

// NewBatchHTTPHandler 创建批量HTTP处理器
// checkpoint: 可选，成功后保存检查点
// retryCount: 失败重试次数，0 表示不重试
// retryDelay: 重试基础延迟（指数退避）
func NewBatchHTTPHandler(apiURL string, timeout time.Duration, batchSize int, flushInterval time.Duration, checkpoint CheckpointSaver, retryCount int, retryDelay time.Duration) *BatchHTTPHandler {
	batchURL := strings.TrimSuffix(apiURL, "/") + "/batch"
	if batchSize <= 0 {
		batchSize = 100
	}
	if batchSize > 100 {
		batchSize = 100
	}
	if flushInterval <= 0 {
		flushInterval = time.Second
	}
	if retryDelay <= 0 {
		retryDelay = time.Second
	}
	h := &BatchHTTPHandler{
		apiURL:        apiURL,
		batchURL:      batchURL,
		client:        NewDefaultHTTPClient(timeout),
		timeout:       timeout,
		batchSize:     batchSize,
		flushInterval: flushInterval,
		retryCount:    retryCount,
		retryDelay:    retryDelay,
		buffer:        make([]filter.MatchResult, 0, batchSize),
		stopChan:      make(chan struct{}),
		checkpoint:    checkpoint,
	}
	h.wg.Add(1)
	go h.flushLoop()
	return h
}

// matchResultToLogItem 将 MatchResult 转为 batch API 的 log 项
func matchResultToLogItem(m filter.MatchResult) map[string]interface{} {
	item := map[string]interface{}{
		"timestamp": time.Now().Unix(),
		"rule_name": m.Rule.Name,
		"rule_desc": m.Rule.Description,
		"log_line":  m.LogLine,
		"log_file":  m.LogFile,
		"pattern":   m.Rule.Pattern,
	}
	if m.Tag != "" {
		item["tag"] = m.Tag
	}
	return item
}

// flush 发送缓冲区中的日志（需持有 mu）
func (h *BatchHTTPHandler) flushLocked() {
	if len(h.buffer) == 0 {
		return
	}
	batch := h.buffer
	h.buffer = make([]filter.MatchResult, 0, h.batchSize)

	logs := make([]map[string]interface{}, 0, len(batch))
	for _, m := range batch {
		logs = append(logs, matchResultToLogItem(m))
	}
	payload := map[string]interface{}{"logs": logs}
	var err error
	for attempt := 0; attempt <= h.retryCount; attempt++ {
		if attempt > 0 {
			delay := h.retryDelay * time.Duration(1<<uint(attempt-1))
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			log.Printf("批量HTTP上报重试 %d/%d，%v 后重试\n", attempt, h.retryCount, delay)
			time.Sleep(delay)
		}
		err = h.client.Post(h.batchURL, payload)
		if err == nil {
			break
		}
	}
	if err != nil {
		atomic.AddInt64(&h.failed, int64(len(batch)))
		log.Printf("批量HTTP上报失败（已重试 %d 次）: %v\n", h.retryCount, err)
	} else {
		atomic.AddInt64(&h.success, int64(len(batch)))
		if h.checkpoint != nil {
			maxOffsetByFile := make(map[string]int64)
			for _, m := range batch {
				if m.Offset > maxOffsetByFile[m.LogFile] {
					maxOffsetByFile[m.LogFile] = m.Offset
				}
			}
			for file, off := range maxOffsetByFile {
				if err := h.checkpoint.SaveMax(file, off); err != nil {
					log.Printf("保存检查点失败 %s: %v\n", file, err)
				}
			}
		}
	}
}

// flushLoop 定时刷新
func (h *BatchHTTPHandler) flushLoop() {
	defer h.wg.Done()
	ticker := time.NewTicker(h.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stopChan:
			h.mu.Lock()
			h.flushLocked()
			h.mu.Unlock()
			return
		case <-ticker.C:
			h.mu.Lock()
			h.flushLocked()
			h.mu.Unlock()
		}
	}
}

// Handle 将匹配结果加入缓冲区，达到批量大小时立即发送
func (h *BatchHTTPHandler) Handle(matchResult filter.MatchResult) error {
	h.mu.Lock()
	h.buffer = append(h.buffer, matchResult)
	shouldFlush := len(h.buffer) >= h.batchSize
	if shouldFlush {
		h.flushLocked()
	}
	h.mu.Unlock()
	return nil
}

// Stop 停止处理器并刷新剩余数据
func (h *BatchHTTPHandler) Stop() {
	close(h.stopChan)
	h.wg.Wait()
}

// GetStats 获取统计信息
func (h *BatchHTTPHandler) GetStats() (success int64, failed int64) {
	return atomic.LoadInt64(&h.success), atomic.LoadInt64(&h.failed)
}

// MultiHandler 多处理器组合
// 可以同时使用多个处理器处理匹配结果
type MultiHandler struct {
	handlers []LogHandler
}

// NewMultiHandler 创建多处理器组合
// handlers: 处理器列表
// 返回: MultiHandler实例
func NewMultiHandler(handlers ...LogHandler) *MultiHandler {
	return &MultiHandler{
		handlers: handlers,
	}
}

// Handle 处理匹配结果，依次调用所有处理器
// matchResult: 匹配结果
// 返回: 错误信息（如果有，返回最后一个错误）
func (h *MultiHandler) Handle(matchResult filter.MatchResult) error {
	var lastErr error
	for _, handler := range h.handlers {
		if err := handler.Handle(matchResult); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// MetricsCollector 指标收集器接口
// 用于统计匹配结果
type MetricsCollector interface {
	IncrementByMatchResult(matchResult filter.MatchResult)
}

// HandlerManager 处理器管理器
// 负责管理日志处理器的创建和运行
type HandlerManager struct {
	handler              LogHandler
	metrics              MetricsCollector
	globalMetricsEnabled bool // 全局指标统计是否启用
	workerNum            int  // worker goroutine 数量
	wg                   sync.WaitGroup
}

// NewHandlerManager 创建处理器管理器
// handler: 日志处理器
// metrics: 指标收集器（可选）
// globalMetricsEnabled: 全局指标统计是否启用
// workerNum: worker goroutine 数量（0表示使用默认值：4）
// 返回: HandlerManager实例
func NewHandlerManager(handler LogHandler, metrics MetricsCollector, globalMetricsEnabled bool, workerNum int) *HandlerManager {
	if workerNum <= 0 {
		workerNum = 4 // 默认使用4个worker，提高并发处理能力
	}

	return &HandlerManager{
		handler:              handler,
		metrics:              metrics,
		globalMetricsEnabled: globalMetricsEnabled,
		workerNum:            workerNum,
	}
}

// Start 启动处理器（使用多个worker并行处理）
// resultChan: 匹配结果通道
// stopChan: 停止信号通道
func (hm *HandlerManager) Start(resultChan <-chan filter.MatchResult, stopChan <-chan struct{}) {
	// 启动多个worker goroutine并行处理
	for i := 0; i < hm.workerNum; i++ {
		hm.wg.Add(1)
		go func(workerID int) {
			defer hm.wg.Done()
			hm.process(resultChan, stopChan)
		}(i)
	}
}

// process 处理匹配结果通道
// resultChan: 匹配结果通道
// stopChan: 停止信号通道
func (hm *HandlerManager) process(resultChan <-chan filter.MatchResult, stopChan <-chan struct{}) {
	for {
		select {
		case <-stopChan:
			return
		case result, ok := <-resultChan:
			if !ok {
				// 通道已关闭
				return
			}

			// 统计指标（如果规则启用了指标统计）
			// 如果规则显式设置了 metrics_enable，使用规则的设置；否则使用全局配置
			if hm.metrics != nil && result.Rule.IsMetricsEnabled(hm.globalMetricsEnabled) {
				hm.metrics.IncrementByMatchResult(result)
			}

			// 若 report_mode 为 metrics_only，只统计指标，不上报完整日志
			if result.Rule.IsReportModeMetricsOnly() {
				continue
			}

			// 上报完整日志（report_mode 为 "full" 或默认）
			if err := hm.handler.Handle(result); err != nil {
				log.Printf("处理匹配结果时出错: %v\n", err)
			}
		}
	}
}

// Wait 等待处理器完成，并停止可停止的处理器（如 BatchHTTPHandler、UDPHandler）
func (hm *HandlerManager) Wait() {
	hm.wg.Wait()
	if batchHandler, ok := hm.handler.(*BatchHTTPHandler); ok {
		batchHandler.Stop()
	}
	if udpHandler, ok := hm.handler.(*UDPHandler); ok {
		udpHandler.Close()
	}
	if tcpHandler, ok := hm.handler.(*TCPHandler); ok {
		tcpHandler.Close()
	}
}

// GetHandler 获取处理器实例
// 返回: 日志处理器
func (hm *HandlerManager) GetHandler() LogHandler {
	return hm.handler
}

// CreateHandler 根据配置创建处理器
// handlerConfig: 处理器配置
// checkpoint: 可选，用于 HTTP 批量模式的断点续传
func CreateHandler(handlerConfig filter.HandlerConfig, checkpoint CheckpointSaver) (LogHandler, error) {
	var timeout time.Duration = 10 * time.Second

	// 解析超时时间
	if handlerConfig.Timeout != "" {
		parsedTimeout, err := time.ParseDuration(handlerConfig.Timeout)
		if err != nil {
			log.Printf("警告：无法解析超时时间 '%s'，使用默认值 10s: %v\n", handlerConfig.Timeout, err)
		} else {
			timeout = parsedTimeout
		}
	}

	switch handlerConfig.Type {
	case "console":
		log.Println("使用控制台输出处理器")
		return NewConsoleHandler(), nil
	case "udp":
		if handlerConfig.UDPAddr == "" {
			return nil, fmt.Errorf("使用UDP处理器时必须在配置文件中配置 udp_addr")
		}
		udpHandler, err := NewUDPHandler(handlerConfig.UDPAddr, handlerConfig.UDPSecret)
		if err != nil {
			return nil, fmt.Errorf("创建UDP处理器失败: %w", err)
		}
		log.Printf("使用UDP上报处理器，目标: %s\n", handlerConfig.UDPAddr)
		return udpHandler, nil
	case "tcp":
		if handlerConfig.TCPAddr == "" {
			return nil, fmt.Errorf("使用TCP处理器时必须在配置文件中配置 tcp_addr")
		}
		batchSize := handlerConfig.TCPBatchSize
		if batchSize <= 0 {
			batchSize = 200 // 性能优化默认值
		}
		flushInterval := 200 * time.Millisecond // 性能优化默认值
		if handlerConfig.TCPFlushInterval != "" {
			if d, err := time.ParseDuration(handlerConfig.TCPFlushInterval); err == nil {
				flushInterval = d
			}
		}
		tcpHandler := NewTCPHandler(handlerConfig.TCPAddr, handlerConfig.TCPSecret, batchSize, flushInterval)
		log.Printf("使用TCP长连接上报处理器，目标: %s，批量: %d，刷新: %v\n", handlerConfig.TCPAddr, batchSize, flushInterval)
		return tcpHandler, nil
	case "http":
		if handlerConfig.APIURL == "" {
			return nil, fmt.Errorf("使用HTTP处理器时必须在配置文件中配置 api_url")
		}
		// 默认使用批量上报以支撑高吞吐（每日上亿级），batch_enabled: false 可切换回逐条
		useBatch := handlerConfig.BatchEnabled == nil || *handlerConfig.BatchEnabled
		if useBatch {
			batchSize := handlerConfig.BatchSize
			if batchSize <= 0 {
				batchSize = 100
			}
			flushInterval := time.Second
			if handlerConfig.BatchInterval != "" {
				if d, err := time.ParseDuration(handlerConfig.BatchInterval); err == nil {
					flushInterval = d
				}
			}
			retryCount := handlerConfig.RetryCount
			if retryCount <= 0 {
				retryCount = 3
			}
			retryDelay := time.Second
			if handlerConfig.RetryBaseDelay != "" {
				if d, e := time.ParseDuration(handlerConfig.RetryBaseDelay); e == nil {
					retryDelay = d
				}
			}
			log.Printf("使用HTTP批量上报处理器，API: %s/batch，批量: %d，刷新: %v，重试: %d 次\n", handlerConfig.APIURL, batchSize, flushInterval, retryCount)
			return NewBatchHTTPHandler(handlerConfig.APIURL, timeout, batchSize, flushInterval, checkpoint, retryCount, retryDelay), nil
		}
		// batch_enabled: false 时使用逐条上报
		log.Printf("使用HTTP上报处理器，API地址: %s，超时时间: %v\n", handlerConfig.APIURL, timeout)
		return NewHTTPHandler(handlerConfig.APIURL, timeout), nil
	default:
		return nil, fmt.Errorf("不支持的处理器类型 '%s'，支持的类型：console, http, udp, tcp", handlerConfig.Type)
	}
}

// Process 处理匹配结果通道（保留向后兼容）
// resultChan: 匹配结果通道
// stopChan: 停止信号通道
// handler: 日志处理器
// metrics: 指标收集器（可选，如果为 nil 则不统计）
// globalMetricsEnabled: 全局指标统计是否启用（默认 true）
func Process(resultChan <-chan filter.MatchResult, stopChan <-chan struct{}, handler LogHandler, metrics MetricsCollector, globalMetricsEnabled bool) {
	manager := NewHandlerManager(handler, metrics, globalMetricsEnabled, 0)
	manager.Start(resultChan, stopChan)
	manager.Wait()
}
