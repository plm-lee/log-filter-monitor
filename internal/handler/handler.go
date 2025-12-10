package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
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
type ConsoleHandler struct {
	mu sync.Mutex // 保护输出操作的互斥锁
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
	h.mu.Lock()
	defer h.mu.Unlock()

	// 输出匹配的日志，包含规则名称和时间戳
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fmt.Printf("[%s] [规则: %s] %s\n", timestamp, matchResult.Rule.Name, matchResult.LogLine)

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
// timeout: 请求超时时间
// 返回: defaultHTTPClient实例
func NewDefaultHTTPClient(timeout time.Duration) *defaultHTTPClient {
	return &defaultHTTPClient{
		client: &http.Client{
			Timeout: timeout,
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
		"pattern":   matchResult.Rule.Pattern,
	}

	// 发送HTTP请求
	err := h.client.Post(h.apiURL, data)
	if err != nil {
		h.mu.Lock()
		h.failed++
		h.mu.Unlock()
		log.Printf("HTTP上报失败: %v\n", err)
		return err
	}

	h.mu.Lock()
	h.success++
	h.mu.Unlock()

	return nil
}

// GetStats 获取统计信息
// 返回: 成功次数和失败次数
func (h *HTTPHandler) GetStats() (success int64, failed int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.success, h.failed
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
	handler LogHandler
	metrics MetricsCollector
	wg      sync.WaitGroup
}

// NewHandlerManager 创建处理器管理器
// handler: 日志处理器
// metrics: 指标收集器（可选）
// 返回: HandlerManager实例
func NewHandlerManager(handler LogHandler, metrics MetricsCollector) *HandlerManager {
	return &HandlerManager{
		handler: handler,
		metrics: metrics,
	}
}

// Start 启动处理器
// resultChan: 匹配结果通道
// stopChan: 停止信号通道
func (hm *HandlerManager) Start(resultChan <-chan filter.MatchResult, stopChan <-chan struct{}) {
	hm.wg.Add(1)
	go func() {
		defer hm.wg.Done()
		hm.process(resultChan, stopChan)
	}()
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

			// 统计指标
			if hm.metrics != nil {
				hm.metrics.IncrementByMatchResult(result)
			}

			// 处理匹配结果
			if err := hm.handler.Handle(result); err != nil {
				log.Printf("处理匹配结果时出错: %v\n", err)
			}
		}
	}
}

// Wait 等待处理器完成
func (hm *HandlerManager) Wait() {
	hm.wg.Wait()
}

// GetHandler 获取处理器实例
// 返回: 日志处理器
func (hm *HandlerManager) GetHandler() LogHandler {
	return hm.handler
}

// CreateHandler 根据配置创建处理器
// handlerConfig: 处理器配置
// 返回: 日志处理器和错误信息
func CreateHandler(handlerConfig filter.HandlerConfig) (LogHandler, error) {
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
	case "http":
		if handlerConfig.APIURL == "" {
			return nil, fmt.Errorf("使用HTTP处理器时必须在配置文件中配置 api_url")
		}
		log.Printf("使用HTTP上报处理器，API地址: %s，超时时间: %v\n", handlerConfig.APIURL, timeout)
		return NewHTTPHandler(handlerConfig.APIURL, timeout), nil
	default:
		return nil, fmt.Errorf("不支持的处理器类型 '%s'，支持的类型：console, http", handlerConfig.Type)
	}
}

// Process 处理匹配结果通道（保留向后兼容）
// resultChan: 匹配结果通道
// stopChan: 停止信号通道
// handler: 日志处理器
// metrics: 指标收集器（可选，如果为 nil 则不统计）
func Process(resultChan <-chan filter.MatchResult, stopChan <-chan struct{}, handler LogHandler, metrics MetricsCollector) {
	manager := NewHandlerManager(handler, metrics)
	manager.Start(resultChan, stopChan)
	manager.Wait()
}
