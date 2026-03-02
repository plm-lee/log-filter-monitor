package handler

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"log-filter-monitor/internal/filter"
)

var lenBufPool = sync.Pool{
	New: func() interface{} { b := make([]byte, 4); return &b },
}

// TCPHandler TCP 长连接上报处理器
type TCPHandler struct {
	addr          string
	secret        string
	host          string
	batchSize     int
	flushInterval time.Duration
	conn          net.Conn
	bufW          *bufio.Writer // 写缓冲，减少 syscall
	connMu        sync.Mutex
	buffer        []filter.MatchResult
	mu            sync.Mutex
	stopChan      chan struct{}
	wg            sync.WaitGroup
	success       int64
	failed        int64
	reportRecorder ReportStatsRecorder
}

// NewTCPHandler 创建 TCP 处理器
func NewTCPHandler(tcpAddr string, tcpSecret string, host string, batchSize int, flushInterval time.Duration, reportRecorder ReportStatsRecorder) *TCPHandler {
	if batchSize <= 0 {
		batchSize = 50
	}
	if flushInterval <= 0 {
		flushInterval = time.Second
	}
	h := &TCPHandler{
		addr:           tcpAddr,
		secret:         tcpSecret,
		host:           host,
		batchSize:      batchSize,
		flushInterval:  flushInterval,
		buffer:         make([]filter.MatchResult, 0, batchSize),
		stopChan:       make(chan struct{}),
		reportRecorder: reportRecorder,
	}
	h.wg.Add(1)
	go h.flushLoop()
	return h
}

func (h *TCPHandler) connect() error {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	if h.conn != nil {
		return nil
	}
	conn, err := net.DialTimeout("tcp", h.addr, 10*time.Second)
	if err != nil {
		return err
	}
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true) // 禁用 Nagle，低延迟
	}
	h.conn = conn
	h.bufW = bufio.NewWriterSize(conn, 64*1024) // 64KB 写缓冲
	return nil
}

func (h *TCPHandler) ensureConn() (net.Conn, error) {
	for attempt := 0; ; attempt++ {
		err := h.connect()
		if err == nil {
			h.connMu.Lock()
			c := h.conn
			h.connMu.Unlock()
			return c, nil
		}
		select {
		case <-h.stopChan:
			return nil, net.ErrClosed
		default:
			delay := time.Second * time.Duration(1<<uint(min(attempt, 6)))
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			log.Printf("TCP 连接失败，%v 后重试: %v\n", delay, err)
			time.Sleep(delay)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (h *TCPHandler) sendFrame(data []byte) error {
	_, err := h.ensureConn()
	if err != nil {
		return err
	}
	h.connMu.Lock()
	w := h.bufW
	conn := h.conn
	h.connMu.Unlock()
	if w == nil || conn == nil {
		return net.ErrClosed
	}
	conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	lenBufPtr := lenBufPool.Get().(*[]byte)
	lenBuf := *lenBufPtr
	binary.BigEndian.PutUint32(lenBuf, uint32(len(data)))
	if _, err := w.Write(lenBuf); err != nil {
		lenBufPool.Put(lenBufPtr)
		h.closeConnLocked(conn)
		return err
	}
	if _, err := w.Write(data); err != nil {
		lenBufPool.Put(lenBufPtr)
		h.closeConnLocked(conn)
		return err
	}
	lenBufPool.Put(lenBufPtr)
	if err := w.Flush(); err != nil {
		h.closeConnLocked(conn)
		return err
	}
	return nil
}

func (h *TCPHandler) closeConnLocked(conn net.Conn) {
	h.connMu.Lock()
	defer h.connMu.Unlock()
	if h.conn == conn {
		if h.conn != nil {
			h.conn.Close()
		}
		h.conn = nil
		h.bufW = nil
	}
}

func (h *TCPHandler) tcpLogItem(m filter.MatchResult) map[string]interface{} {
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
	if h.host != "" {
		item["host"] = h.host
	}
	if h.secret != "" {
		item["secret"] = h.secret
	}
	return item
}

func (h *TCPHandler) flushLocked() {
	if len(h.buffer) == 0 {
		return
	}
	batch := h.buffer
	h.buffer = make([]filter.MatchResult, 0, h.batchSize)

	logs := make([]map[string]interface{}, 0, len(batch))
	for _, m := range batch {
		logs = append(logs, h.tcpLogItem(m))
	}
	payload := map[string]interface{}{"logs": logs}
	data, err := json.Marshal(payload)
	if err != nil {
		atomic.AddInt64(&h.failed, int64(len(batch)))
		log.Printf("TCP 序列化失败: %v\n", err)
		return
	}
	count := int64(len(batch))
	start := time.Now()
	if err := h.sendFrame(data); err != nil {
		atomic.AddInt64(&h.failed, count)
		log.Printf("TCP 发送失败: %v\n", err)
	} else {
		atomic.AddInt64(&h.success, count)
		if h.reportRecorder != nil {
			h.reportRecorder.RecordReport(time.Since(start), count)
		}
	}
}

func (h *TCPHandler) flushLoop() {
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

// Handle 实现 LogHandler 接口，将匹配结果加入缓冲区
func (h *TCPHandler) Handle(matchResult filter.MatchResult) error {
	h.mu.Lock()
	h.buffer = append(h.buffer, matchResult)
	shouldFlush := len(h.buffer) >= h.batchSize
	if shouldFlush {
		h.flushLocked()
	}
	h.mu.Unlock()
	return nil
}

// Close 关闭 TCP 连接
func (h *TCPHandler) Close() error {
	close(h.stopChan)
	h.wg.Wait()
	h.connMu.Lock()
	defer h.connMu.Unlock()
	if h.conn != nil {
		err := h.conn.Close()
		h.conn = nil
		h.bufW = nil
		return err
	}
	return nil
}

// GetStats 获取统计信息
func (h *TCPHandler) GetStats() (success int64, failed int64) {
	return atomic.LoadInt64(&h.success), atomic.LoadInt64(&h.failed)
}
