package handler

import (
	"encoding/json"
	"log"
	"net"
	"sync/atomic"
	"time"

	"log-filter-monitor/internal/filter"
)

const maxUDPPayloadSize = 1400 // 留出 IP+UDP 头空间，避免分片

// UDPHandler UDP 上报处理器
type UDPHandler struct {
	conn           *net.UDPConn
	addr           *net.UDPAddr
	secret         string
	host           string
	success        int64
	failed         int64
	reportRecorder ReportStatsRecorder
}

// NewUDPHandler 创建 UDP 处理器
func NewUDPHandler(udpAddr string, udpSecret string, host string, reportRecorder ReportStatsRecorder) (*UDPHandler, error) {
	addr, err := net.ResolveUDPAddr("udp", udpAddr)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, err
	}
	return &UDPHandler{
		conn:           conn,
		addr:           addr,
		secret:         udpSecret,
		host:           host,
		reportRecorder: reportRecorder,
	}, nil
}

// matchResultToPayload 将 MatchResult 转为与 ReceiveLogRequest 兼容的 JSON 结构
func (h *UDPHandler) matchResultToPayload(m filter.MatchResult) map[string]interface{} {
	payload := map[string]interface{}{
		"timestamp": time.Now().Unix(),
		"rule_name": m.Rule.Name,
		"rule_desc": m.Rule.Description,
		"log_line":  m.LogLine,
		"log_file":  m.LogFile,
		"pattern":   m.Rule.Pattern,
	}
	if m.Tag != "" {
		payload["tag"] = m.Tag
	}
	if h.host != "" {
		payload["host"] = h.host
	}
	if h.secret != "" {
		payload["secret"] = h.secret
	}
	return payload
}

// Handle 处理匹配结果，通过 UDP 发送
func (h *UDPHandler) Handle(matchResult filter.MatchResult) error {
	payload := h.matchResultToPayload(matchResult)
	data, err := json.Marshal(payload)
	if err != nil {
		atomic.AddInt64(&h.failed, 1)
		log.Printf("UDP 序列化失败: %v\n", err)
		return err
	}
	if len(data) > maxUDPPayloadSize {
		// 超长时截断 log_line 并重新序列化
		logLine, _ := payload["log_line"].(string)
		maxLineLen := maxUDPPayloadSize - 200 // 预留其他字段和 JSON 结构
		if len(logLine) > maxLineLen {
			payload["log_line"] = logLine[:maxLineLen] + "...[truncated]"
			data, _ = json.Marshal(payload)
			log.Printf("UDP 日志过长已截断，原始 %d 字节\n", len(logLine))
		}
	}
	start := time.Now()
	_, err = h.conn.Write(data)
	if err != nil {
		atomic.AddInt64(&h.failed, 1)
		log.Printf("UDP 发送失败: %v\n", err)
		return err
	}
	atomic.AddInt64(&h.success, 1)
	if h.reportRecorder != nil {
		h.reportRecorder.RecordReport(time.Since(start), 1)
	}
	return nil
}

// Close 关闭 UDP 连接
func (h *UDPHandler) Close() error {
	if h.conn != nil {
		return h.conn.Close()
	}
	return nil
}

// GetStats 获取统计信息
func (h *UDPHandler) GetStats() (success int64, failed int64) {
	return atomic.LoadInt64(&h.success), atomic.LoadInt64(&h.failed)
}
